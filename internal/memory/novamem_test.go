package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	novamem "github.com/azrtydxb/novamem/clients/go"
)

func isolationScopes() []Scope {
	base := Scope{"tenant", "organization", "telecom", "namespace", "user"}
	out := []Scope{base, base, base, base, base, base}
	out[1].Tenant = "other"
	out[2].Organization = "other"
	out[3].Domain = "hospital"
	out[4].Namespace = "other"
	out[5].User = "other"
	return out
}

func TestNovaMemIsolationAndRestart(t *testing.T) {
	scopes := isolationScopes()
	credentials := []ScopedCredential{}
	owners := map[string]Scope{}
	for i, s := range scopes {
		token := fmt.Sprintf("token-%d", i)
		credentials = append(credentials, ScopedCredential{s, token})
		owners["Bearer "+token] = s
	}
	var mu sync.Mutex
	stored := map[string][]novamem.Entry{}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		scope, ok := owners[r.Header.Get("Authorization")]
		if !ok {
			t.Error("wrong scoped credential")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/remember":
			var in novamem.CaptureRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.Namespace != ScopeKey(scope) || in.Project != "" || in.Source != novaSource {
				t.Error("unscoped write")
			}
			entries := stored[in.Namespace]
			if len(entries) == 0 {
				stored[in.Namespace] = []novamem.Entry{{ID: "one", Score: 1, Signals: &novamem.Signals{Keyword: 1}, Content: in.Content, Namespace: in.Namespace, Source: in.Source, Metadata: in.Metadata}}
			}
			fmt.Fprint(w, `{"id":"one","deduplicated":true}`)
		case "/v1/search":
			var in novamem.SearchRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.Namespace != ScopeKey(scope) || in.K != 5 || len(in.IncludeNamespaces) != 0 || in.Project != "" || len(in.IncludeProjects) != 0 {
				t.Error("unscoped search")
			}
			if in.Weights == nil || *in.Weights.Recency != 0 {
				t.Error("recency-only memories must not be relevant")
			}
			json.NewEncoder(w).Encode(novamem.Results{Entries: stored[in.Namespace]})
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	cfg := NovaMemConfig{BaseURL: server.URL, Credentials: credentials}
	p, err := NewNovaMem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fact := Memory{Scope: scopes[0], Text: "Recurring upstairs Wi-Fi interference.", Tags: []string{"wifi"}}
	for range 2 {
		if err := p.Store(ctx, fact); err != nil {
			t.Fatal(err)
		}
	}
	// A new provider has no process-local state to rescue a missing durable write.
	p, err = NewNovaMem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	own, err := p.Retrieve(ctx, RetrieveRequest{Scope: scopes[0], Query: "wifi", Limit: 200})
	if err != nil || len(own) != 1 || own[0].Text != fact.Text || own[0].Created.IsZero() {
		t.Fatal(own, err)
	}
	for _, scope := range scopes[1:] {
		got, err := p.Retrieve(ctx, RetrieveRequest{Scope: scope, Query: "wifi"})
		if err != nil || len(got) != 0 {
			t.Fatalf("scope leaked %+v: %+v %v", scope, got, err)
		}
	}
	missing := scopes[0]
	missing.User = "unprovisioned"
	before := requests
	if _, err = p.Retrieve(ctx, RetrieveRequest{Scope: missing, Query: "wifi"}); err == nil || requests != before {
		t.Fatal("missing identity did not fail closed")
	}
}

func TestNovaMemRejectsCrossScopeResults(t *testing.T) {
	scopes := isolationScopes()
	for _, wrong := range scopes[1:] {
		t.Run(ScopeKey(wrong), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"content": "Must not reach the model", "score": 1, "namespace": ScopeKey(scopes[0]), "source": novaSource, "metadata": map[string]any{"enterprise_demo": novaMetadata{Version: 1, Scope: wrong}}}}})
			}))
			defer server.Close()
			p, err := NewNovaMem(NovaMemConfig{BaseURL: server.URL, Credentials: []ScopedCredential{{scopes[0], "token"}}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Retrieve(context.Background(), RetrieveRequest{Scope: scopes[0], Query: "wifi"})
			if err == nil || len(got) != 0 {
				t.Fatal("foreign memory accepted", got, err)
			}
		})
	}
}

// NovaMem's derived facts carry no enterprise_demo metadata; they must still be
// recalled from the scope's own namespace, and refused from any other.
func TestNovaMemAcceptsDerivedFactsOnlyInOwnNamespace(t *testing.T) {
	scope := isolationScopes()[0]
	for _, tc := range []struct {
		namespace string
		ok        bool
	}{{ScopeKey(scope), true}, {ScopeKey(isolationScopes()[5]), false}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"content": "[event] Sarah toured the school", "score": 1, "signals": map[string]any{"keyword": 1}, "namespace": tc.namespace, "source": novaSource}}})
		}))
		p, err := NewNovaMem(NovaMemConfig{BaseURL: server.URL, Credentials: []ScopedCredential{{scope, "token"}}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Retrieve(context.Background(), RetrieveRequest{Scope: scope, Query: "tour"})
		server.Close()
		if tc.ok && (err != nil || len(got) != 1 || got[0].Text != "[event] Sarah toured the school") {
			t.Fatal("derived fact not recalled", got, err)
		}
		if !tc.ok && (err == nil || len(got) != 0) {
			t.Fatal("foreign-namespace fact accepted", got, err)
		}
	}
}

func TestNovaMemErrorsAndCancellation(t *testing.T) {
	scope := isolationScopes()[0]
	for _, body := range []string{`{"results":[],"degraded":true}`, `not-json`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		p, _ := NewNovaMem(NovaMemConfig{BaseURL: s.URL, Credentials: []ScopedCredential{{scope, "token"}}})
		if _, err := p.Retrieve(context.Background(), RetrieveRequest{Scope: scope, Query: "wifi"}); err == nil {
			t.Fatal("outage presented as empty memory")
		}
		s.Close()
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	p, _ := NewNovaMem(NovaMemConfig{BaseURL: s.URL, Credentials: []ScopedCredential{{scope, "token"}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Retrieve(ctx, RetrieveRequest{Scope: scope, Query: "wifi"}); err == nil {
		t.Fatal("ignored cancellation")
	}
	for _, credentials := range [][]ScopedCredential{{{scope, ""}}, {{scope, "token"}, {isolationScopes()[1], "token"}}, {{Scope{}, "token"}}} {
		if _, err := NewNovaMem(NovaMemConfig{BaseURL: s.URL, Credentials: credentials}); err == nil {
			t.Fatal("invalid credential configuration accepted")
		}
	}
}

func TestScopeKeyUnambiguous(t *testing.T) {
	a := Scope{"t|o", "a", "d", "n", "u"}
	b := Scope{"t", "o|a", "d", "n", "u"}
	if ScopeKey(a) == ScopeKey(b) || len(ScopeKey(a)) > 128 || strings.Contains(ScopeKey(a), "|") {
		t.Fatal("ambiguous scope encoding")
	}
}

func TestNovaMemWriteRejected(t *testing.T) {
	scope := isolationScopes()[0]
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"rejected":"not saved"}`) }))
	defer s.Close()
	p, _ := NewNovaMem(NovaMemConfig{BaseURL: s.URL, Credentials: []ScopedCredential{{scope, "token"}}, Timeout: time.Second})
	if err := p.Store(context.Background(), Memory{Scope: scope, Text: "User prefers morning appointments."}); err == nil {
		t.Fatal("false storage success")
	}
}

func TestNovaMemFiltersWeakSemanticMatches(t *testing.T) {
	scope := isolationScopes()[0]
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries := []novamem.Entry{}
		for i, signal := range []novamem.Signals{{Vector: 0.42}, {Vector: 0.6}, {Keyword: 1, Vector: 0.3}} {
			entries = append(entries, novamem.Entry{ID: fmt.Sprint(i), Score: 0.4, Content: fmt.Sprint(i), Namespace: ScopeKey(scope), Source: novaSource, Signals: &signal, Metadata: map[string]any{"enterprise_demo": novaMetadata{Version: 1, Scope: scope}}})
		}
		json.NewEncoder(w).Encode(novamem.Results{Entries: entries})
	}))
	defer s.Close()
	for _, cutoff := range []float64{0.5, 0.7} {
		p, err := NewNovaMem(NovaMemConfig{BaseURL: s.URL, Credentials: []ScopedCredential{{scope, "token"}}, MinVectorScore: &cutoff})
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Retrieve(context.Background(), RetrieveRequest{Scope: scope, Query: "appointment"})
		want := 2
		if cutoff == 0.7 {
			want = 1
		}
		if err != nil || len(got) != want {
			t.Fatal(got, err)
		}
		for _, m := range got {
			if m.Text == "0" {
				t.Fatal("weak unrelated memory leaked into context")
			}
		}
	}
}
