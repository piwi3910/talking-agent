package memory

import (
	"context"
	"testing"
)

func TestIsolationEveryScopeDimension(t *testing.T) {
	p := New()
	ctx := context.Background()
	base := Scope{"tenant", "org", "telecom", "namespace", "user"}
	if err := p.Store(ctx, Memory{Scope: base, Text: "Upstairs wifi interference", Tags: []string{"wifi"}}); err != nil {
		t.Fatal(err)
	}
	scopes := []Scope{base, base, base, base, base}
	scopes[0].Tenant = "other"
	scopes[1].Organization = "other"
	scopes[2].Domain = "school"
	scopes[3].Namespace = "other"
	scopes[4].User = "other"
	for _, scope := range scopes {
		got, err := p.Retrieve(ctx, RetrieveRequest{Scope: scope, Query: "wifi"})
		if err != nil || len(got) != 0 {
			t.Fatalf("scope leaked: %+v, %+v, %v", scope, got, err)
		}
	}
	got, _ := p.Retrieve(ctx, RetrieveRequest{Scope: base, Query: "wifi"})
	if len(got) != 1 {
		t.Fatal("expected own memory")
	}
	irrelevant, _ := p.Retrieve(ctx, RetrieveRequest{Scope: base, Query: "insurance"})
	if len(irrelevant) != 0 {
		t.Fatal("irrelevant memory returned")
	}
}
func TestPreferenceRetrievalAndDeduplication(t *testing.T) {
	p := New()
	s := Scope{"t", "o", "school", "n", "u"}
	ms := Extract(s, "Mornings normally work better for me.", []Rule{{Pattern: `(?i)morning`, Text: "User prefers morning appointments.", Tags: []string{"appointment", "ahmed"}}})
	if len(ms) != 1 {
		t.Fatal(ms)
	}
	for i := 0; i < 2; i++ {
		if err := p.Store(context.Background(), ms[0]); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := p.Retrieve(context.Background(), RetrieveRequest{Scope: s, Query: "Can I make another appointment with Dr. Ahmed?"})
	if len(got) != 1 {
		t.Fatal(got)
	}
	if len(Extract(s, "hello", nil)) != 0 {
		t.Fatal("stored meaningless fragment")
	}
}
func TestNovaMemExplicitlyUnavailable(t *testing.T) {
	_, err := (NovaMemProvider{}).Retrieve(context.Background(), RetrieveRequest{})
	if err != ErrNotConfigured {
		t.Fatal(err)
	}
}
