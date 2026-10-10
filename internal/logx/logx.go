// Package logx contains shared logging context and credential redaction helpers.
package logx

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

type contextKey struct{}

type correlation struct {
	session string
	turn    string
}

// WithCorrelation attaches session and turn identifiers to log records emitted
// with this context.
func WithCorrelation(ctx context.Context, sessionID, turnID string) context.Context {
	return context.WithValue(ctx, contextKey{}, correlation{session: sessionID, turn: turnID})
}

var (
	bearerPattern = regexp.MustCompile(`(?i)(\bbearer\s+)[^\s,;"']+`)
	queryPattern  = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|key|secret|password)=)[^&#\s"']*`)
	assignPattern = regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|client[_-]?secret|password)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	authPattern   = regexp.MustCompile(`(?i)(\b(?:[\w-]*api[_-]?key|[\w-]*token|authorization|[\w-]*secret|password)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	urlPattern    = regexp.MustCompile(`https?://[^\s<>"']+`)
)

// Redact removes common credentials from arbitrary error text before it is
// written to logs or telemetry.
func Redact(text string) string {
	text = bearerPattern.ReplaceAllString(text, `${1}***`)
	text = queryPattern.ReplaceAllString(text, `${1}***`)
	text = assignPattern.ReplaceAllString(text, `${1}***`)
	text = authPattern.ReplaceAllString(text, `${1}***`)
	return urlPattern.ReplaceAllStringFunc(text, func(raw string) string {
		trimmed := strings.TrimRight(raw, ".,);]")
		suffix := raw[len(trimmed):]
		u, err := url.Parse(trimmed)
		if err != nil || u.Host == "" {
			return raw
		}
		changed := false
		if u.User != nil {
			u.User = url.UserPassword("***", "***")
			changed = true
		}
		q := u.Query()
		for k := range q {
			if sensitiveName(k) {
				q.Set(k, "***")
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
		}
		return u.String() + suffix
	})
}

// Error returns a redacted error string, preserving nil as an empty string.
func Error(err error) string {
	if err == nil {
		return ""
	}
	return Redact(err.Error())
}

// RedactValue recursively sanitizes a JSON-shaped telemetry payload.
func RedactValue(value any) any {
	switch v := value.(type) {
	case error:
		return Error(v)
	case string:
		return Redact(v)
	case []string:
		out := make([]string, len(v))
		for i := range v {
			out[i] = Redact(v[i])
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = RedactValue(v[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = RedactValue(item)
		}
		return out
	case nil, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return value
	default:
		b, err := json.Marshal(value)
		if err != nil {
			return value
		}
		var normalized any
		if json.Unmarshal(b, &normalized) != nil {
			return value
		}
		if normalized == nil {
			return value
		}
		return RedactValue(normalized)
	}
}

func sensitiveName(name string) bool {
	n := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(name))
	return n == "apikey" || n == "token" || n == "accesstoken" || n == "refreshtoken" || n == "key" || n == "secret" || n == "password" || n == "authorization"
}

// Handler adds correlation fields from context and redacts every string/error
// attribute, providing a final safety net for slog call sites.
type Handler struct{ slog.Handler }

func NewHandler(h slog.Handler) slog.Handler { return Handler{Handler: h} }

func (h Handler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make([]slog.Attr, 0, 2)
	if c, ok := ctx.Value(contextKey{}).(correlation); ok {
		if c.session != "" {
			attrs = append(attrs, slog.String("session_id", c.session))
		}
		if c.turn != "" {
			attrs = append(attrs, slog.String("turn_id", c.turn))
		}
	}
	r2 := slog.NewRecord(r.Time, r.Level, Redact(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		r2.AddAttrs(redactAttr(a))
		return true
	})
	for _, a := range attrs {
		r2.AddAttrs(a)
	}
	return h.Handler.Handle(ctx, r2)
}

func (h Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = redactAttr(a)
	}
	return Handler{Handler: h.Handler.WithAttrs(clean)}
}

func (h Handler) WithGroup(name string) slog.Handler {
	return Handler{Handler: h.Handler.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(Redact(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(Error(err))
		} else if s, ok := a.Value.Any().(string); ok {
			a.Value = slog.StringValue(Redact(s))
		}
	case slog.KindGroup:
		group := a.Value.Group()
		for i := range group {
			group[i] = redactAttr(group[i])
		}
		a.Value = slog.GroupValue(group...)
	}
	return a
}
