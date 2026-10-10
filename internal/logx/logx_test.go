package logx

import (
	"errors"
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
	for _, expected := range []string{"Bearer ***", "api_key=***", "example.test", "access_token"} {
		if !strings.Contains(got, expected) {
			t.Errorf("redacted error missing %q: %s", expected, got)
		}
	}
}
