package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidation(t *testing.T) {
	d := Definition{Input: Schema{Type: "object", Properties: map[string]Property{"channel": {Type: "string", Enum: []string{"1", "6", "11"}}}, Required: []string{"channel"}}}
	for _, raw := range []string{`{"channel":11}`, `{"channel":null}`, `{"channel":"7"}`, `{"channel":"11","user_id":"someone-else"}`, `{}`, `null`, `{"channel":"11"} {}`} {
		if _, e := d.Validate(raw); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, e := d.Validate(`{"channel":"11"}`); e != nil {
		t.Fatal(e)
	}
}
func TestTimeoutAndEvents(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer s.Close()
	h := HTTPExecutor{BaseURL: s.URL}
	events := []string{}
	begin := time.Now()
	r := h.Execute(context.Background(), Definition{Name: "slow", TimeoutMS: 25}, Request{}, func(kind string, _ any) { events = append(events, kind) })
	if r.Error == nil || time.Since(begin) > time.Second {
		t.Fatal(r)
	}
	if len(events) != 2 || events[0] != "tool.started" || events[1] != "tool.failed" {
		t.Fatal(events)
	}
}
