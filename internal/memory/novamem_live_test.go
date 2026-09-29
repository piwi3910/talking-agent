package memory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	novamem "github.com/azrtydxb/novamem/clients/go"
)

// Opt-in contract check against disposable validation accounts, never production users.
func TestNovaMemLiveIsolation(t *testing.T) {
	path := os.Getenv("NOVAMEM_LIVE_CREDENTIALS")
	if path == "" {
		t.Skip("set NOVAMEM_LIVE_CREDENTIALS to validation-only scoped credentials")
	}
	credentials, err := LoadNovaMemCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 6 {
		t.Fatal("expected six isolation-test accounts")
	}
	for _, c := range credentials {
		if !strings.HasPrefix(c.Scope.Tenant, "enterprise-demo-validation") {
			t.Fatal("refusing non-validation account")
		}
	}
	cfg := NovaMemConfig{BaseURL: os.Getenv("NOVAMEM_LIVE_URL"), Credentials: credentials, Timeout: 10 * time.Second}
	p, err := NewNovaMem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	text := fmt.Sprintf("Validation customer prefers morning appointments; upstairs Wi-Fi interference recurs. Run %d.", time.Now().UnixNano())
	base := credentials[0].Scope
	c, err := novamem.New(novamem.Config{BaseURL: cfg.BaseURL, Token: credentials[0].Token, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		recent, err := c.Recent(ctx, novamem.RecentRequest{Namespace: ScopeKey(base), K: 100})
		if err != nil {
			t.Error(err)
			return
		}
		for _, e := range recent.Entries {
			if e.Content == text {
				result, err := c.Forget(ctx, novamem.ForgetRequest{ID: e.ID})
				if err != nil || !result.Deleted || !result.ColdDeleteOk {
					t.Errorf("validation cleanup incomplete: %+v %v", result, err)
				}
			}
		}
	})
	for range 2 {
		if err := p.Store(ctx, Memory{Scope: base, Text: text, Tags: []string{"appointment", "wifi"}}); err != nil {
			t.Fatal(err)
		}
	}
	// Reconstruct the provider to prove retrieval does not depend on its local heap.
	p, err = NewNovaMem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Retrieve(ctx, RetrieveRequest{Scope: base, Query: "morning appointments upstairs Wi-Fi interference", Limit: 5})
	if err != nil || len(got) != 1 || got[0].Text != text {
		t.Fatalf("own durable fact: %+v %v", got, err)
	}
	for _, credential := range credentials[1:] {
		got, err := p.Retrieve(ctx, RetrieveRequest{Scope: credential.Scope, Query: "morning appointments upstairs Wi-Fi interference", Limit: 5})
		if err != nil || len(got) != 0 {
			t.Fatalf("scope isolation failed: %+v %+v %v", credential.Scope, got, err)
		}
	}
}
