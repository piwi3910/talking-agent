package speech

import (
	"context"
	"enterprise-ai-demo/internal/logx"
)

// TraceFunc receives diagnostic events about one upstream speech request.
type TraceFunc func(event string, data map[string]any)

type traceKey struct{}

// WithTrace attaches a trace sink to ctx; synthesis reports upstream timings to it.
func WithTrace(ctx context.Context, f TraceFunc) context.Context {
	return context.WithValue(ctx, traceKey{}, f)
}

func trace(ctx context.Context, event string, data map[string]any) {
	if f, ok := ctx.Value(traceKey{}).(TraceFunc); ok && f != nil {
		if clean, ok := logx.RedactValue(data).(map[string]any); ok {
			f(event, clean)
		} else {
			f(event, data)
		}
	}
}
