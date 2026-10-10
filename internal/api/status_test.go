package api

import (
	"encoding/json"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCancelReturnsNoContent(t *testing.T) {
	store := session.NewStore()
	s := store.Create(&config.Agent{ID: "agent"}, "user")
	h := (&API{Sessions: store}).Handler()
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/"+s.ID+"/cancel", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("cancel response: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestErrorResponseShapeTable(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 413, 429, 500, 502, 503} {
		t.Run(httpStatusName(status), func(t *testing.T) {
			w := httptest.NewRecorder()
			fail(w, status, "example failure")
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != status || len(body) != 1 || body["error"] != "example failure" || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected error response: status=%d headers=%v body=%v", w.Code, w.Header(), body)
			}
		})
	}
}

func httpStatusName(status int) string {
	return map[int]string{400: "bad_request", 401: "unauthorized", 403: "forbidden", 404: "not_found", 409: "conflict", 413: "too_large", 429: "rate_limited", 500: "internal", 502: "upstream", 503: "unavailable"}[status]
}
