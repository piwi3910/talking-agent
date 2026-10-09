package mock

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestToolLatencyRanges(t *testing.T) {
	cases := []struct {
		tool     string
		min, max time.Duration
	}{
		{"network.diagnostics", 2500 * time.Millisecond, 6 * time.Second},
		{"network.speedtest", 2500 * time.Millisecond, 6 * time.Second},
		{"wifi.optimize", 2500 * time.Millisecond, 6 * time.Second},
		{"wifi.restart", 2500 * time.Millisecond, 6 * time.Second},
		{"network.outages", 600 * time.Millisecond, 1500 * time.Millisecond},
		{"customer.profile", 600 * time.Millisecond, 1500 * time.Millisecond},
		{"crm.history", 600 * time.Millisecond, 1500 * time.Millisecond},
		{"tour.availability", time.Second, 2500 * time.Millisecond},
		{"technician.book", time.Second, 2500 * time.Millisecond},
		{"admissions.fee_quote", 500 * time.Millisecond, time.Second},
	}
	for _, tc := range cases {
		seen := map[time.Duration]bool{}
		for i := 0; i < 40; i++ {
			d := Delay(tc.tool, 1)
			if d < tc.min || d > tc.max {
				t.Fatalf("%s took %s, want %s..%s", tc.tool, d, tc.min, tc.max)
			}
			seen[d] = true
		}
		if len(seen) < 5 {
			t.Fatalf("%s latency has no jitter", tc.tool)
		}
	}
	if Delay("network.diagnostics", 0) != 0 {
		t.Fatal("scale 0 must disable latency")
	}
	if got := Delay("wifi.restart", 0.5); got < 2*time.Second || got > 3*time.Second {
		t.Fatalf("scaled latency %s", got)
	}
}

func TestHandlerHonoursLatency(t *testing.T) {
	b := fixture(t)
	b.LatencyScale = 0.02 // diagnostics: 70 to 120 ms
	srv := httptest.NewServer(b.Handler())
	defer srv.Close()
	start := time.Now()
	resp, err := http.Post(srv.URL+"/tools/execute", "application/json", strings.NewReader(`{"industry":"telecom","user_id":"C001","name":"network.diagnostics","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if took := time.Since(start); took < 60*time.Millisecond {
		t.Fatalf("answered in %s, latency not applied", took)
	}
}

func TestHandlerLatencyIsCancellable(t *testing.T) {
	b := fixture(t)
	b.LatencyScale = 1
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/tools/execute", bytes.NewBufferString(`{"industry":"telecom","user_id":"C001","name":"wifi.restart","arguments":{}}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { b.Handler().ServeHTTP(rec, req); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled request kept waiting")
	}
	if rec.Body.Len() != 0 {
		t.Fatal("a cancelled call must not execute or answer")
	}
	// The mutation must not have run.
	if b.users["telecom"]["C001"].Router != "healthy" {
		t.Fatal("router state changed")
	}
}

func TestRichTelecomResults(t *testing.T) {
	b := fixture(t)
	has := func(rs []string, want ...string) {
		t.Helper()
		for _, w := range want {
			found := false
			for _, r := range rs {
				found = found || strings.Contains(r, w)
			}
			if !found {
				t.Fatalf("missing %q in %v", w, rs)
			}
		}
	}
	diag := call(b, "telecom", "C006", "network.diagnostics", nil)
	names := []string{}
	for _, r := range diag.Records {
		names = append(names, r.Name)
	}
	has(names, "Packet loss", "Latency", "Jitter", "Optical signal", "Line sync download")
	if !strings.Contains(diag.Summary, "wifi.restart") || !strings.Contains(diag.Summary, "packet loss") {
		t.Fatal(diag.Summary)
	}
	fault := call(b, "telecom", "C007", "network.diagnostics", nil)
	if !strings.Contains(fault.Summary, "technician") || !strings.Contains(fault.Summary, "dBm") || !strings.Contains(fault.Summary, "technician.availability") {
		t.Fatal(fault.Summary)
	}
	speed := call(b, "telecom", "C001", "network.speedtest", nil)
	if speed.Error != nil || !strings.Contains(speed.Summary, "Upload") || !strings.Contains(speed.Summary, "ping") || len(speed.Records) < 4 || speed.Records[0].Kind != "speedtest" {
		t.Fatalf("%+v", speed)
	}
	if out := call(b, "telecom", "C002", "network.outages", nil); !strings.Contains(out.Summary, "fault reference INC-") || !strings.Contains(out.Summary, "expected back around") || out.Records[0].ID != "OUT-001" {
		t.Fatalf("%+v", out)
	}
	wifi := call(b, "telecom", "C001", "wifi.diagnostics", nil)
	if !strings.Contains(wifi.Summary, "dBm") || !strings.Contains(wifi.Summary, "wifi.optimize") || wifi.Records[0].Status != "interference" {
		t.Fatalf("%+v", wifi)
	}
	// Same customer, same line; different customers vary.
	a, c := call(b, "telecom", "C001", "network.diagnostics", nil), call(b, "telecom", "C001", "network.diagnostics", nil)
	if a.Summary != c.Summary {
		t.Fatal("line snapshot must be stable per customer")
	}
	seen := map[string]bool{}
	for _, u := range b.Users("telecom") {
		if r := call(b, "telecom", u.ID, "network.diagnostics", nil); r.Error == nil {
			seen[r.Summary] = true
		}
	}
	if len(seen) < 8 {
		t.Fatalf("only %d distinct diagnostics across customers", len(seen))
	}
	restart := call(b, "telecom", "C006", "wifi.restart", nil)
	if restart.Error != nil || !strings.Contains(restart.Summary, "seconds") || !strings.Contains(restart.Summary, "Router health is now normal") {
		t.Fatalf("%+v", restart)
	}
}

func TestBookingsCarryConfirmationAndNextSteps(t *testing.T) {
	b := fixture(t)
	slots := call(b, "aquila", "L001", "tour.availability", nil)
	if len(slots.Records) == 0 {
		t.Fatal(slots)
	}
	book := call(b, "aquila", "L001", "tour.book", map[string]string{"slot_id": slots.Records[0].ID})
	if book.Error != nil || !strings.Contains(book.Summary, "Tour booked") || !strings.Contains(book.Summary, "confirmation number is ") {
		t.Fatalf("%+v", book)
	}
	if confirmationNo("TOUR-1003") != confirmationNo("TOUR-1003") || len(confirmationNo("TOUR-1003")) != 5 {
		t.Fatal("confirmation number must be a stable 5-digit number")
	}
	tech := call(b, "telecom", "C001", "technician.availability", nil)
	tb := call(b, "telecom", "C001", "technician.book", map[string]string{"slot_id": tech.Records[0].ID})
	if tb.Error != nil || !strings.Contains(tb.Summary, "confirmation number") || !strings.Contains(tb.Summary, "phones about 30 minutes") {
		t.Fatalf("%+v", tb)
	}
}
