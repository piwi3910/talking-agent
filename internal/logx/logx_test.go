package logx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestErrorRedactsCredentials(t *testing.T) {
	err := errors.New(`request failed: Authorization: Bearer fake-bearer-value api_key=fake-api-key https://user:pass@example.test/path?access_token=fake-query-token`)
	got := Error(err)
	for _, secret := range []string{"fake-bearer-value", "fake-api-key", "user:pass", "fake-query-token"} {
		if strings.Contains(got, secret) {
			t.Errorf("redacted error contains credential %q: %s", secret, got)
		}
	}
	for _, expected := range []string{"Authorization: ***", "api_key=***", "example.test", "access_token"} {
		if !strings.Contains(got, expected) {
			t.Errorf("redacted error missing %q: %s", expected, got)
		}
	}
}

func TestHandlerAddsCorrelationFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&output, nil)))
	ctx := WithCorrelation(context.Background(), "session-123", "turn-456")
	logger.InfoContext(ctx, "turn started")
	for _, field := range []string{`"session_id":"session-123"`, `"turn_id":"turn-456"`} {
		if !strings.Contains(output.String(), field) {
			t.Errorf("log record missing %s: %s", field, output.String())
		}
	}
}
