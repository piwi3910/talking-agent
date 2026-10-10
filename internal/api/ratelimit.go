package api

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	at     time.Time
}
type tokenBuckets struct {
	mu      sync.Mutex
	entries map[string]bucket
}

func (b *tokenBuckets) allow(key string, rate, burst float64) (bool, time.Duration) {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = map[string]bucket{}
	}
	x := b.entries[key]
	if x.at.IsZero() {
		x = bucket{tokens: burst, at: now}
	}
	x.tokens += now.Sub(x.at).Seconds() * rate
	if x.tokens > burst {
		x.tokens = burst
	}
	x.at = now
	if x.tokens >= 1 {
		x.tokens--
		b.entries[key] = x
		return true, 0
	}
	wait := time.Duration((1 - x.tokens) / rate * float64(time.Second))
	b.entries[key] = x
	return false, wait
}

type requestLimiter struct {
	buckets                                                             tokenBuckets
	ipRate, ipBurst, messageRate, messageBurst, speechRate, speechBurst float64
}

func envFloat(name string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil && v > 0 {
		return v
	}
	return fallback
}
func newRequestLimiter() *requestLimiter {
	return &requestLimiter{ipRate: envFloat("RATE_IP_PER_SECOND", 20), ipBurst: envFloat("RATE_IP_BURST", 120), messageRate: envFloat("RATE_MESSAGES_PER_SECOND", 5), messageBurst: envFloat("RATE_MESSAGES_BURST", 10), speechRate: envFloat("RATE_SPEECH_PER_SECOND", 3), speechBurst: envFloat("RATE_SPEECH_BURST", 6)}
}
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func (l *requestLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := "ip:" + clientIP(r)
		if strings.HasPrefix(r.URL.Path, "/api/sessions/") {
			id := r.PathValue("id")
			switch {
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
				key = "messages:" + id
				if ok, wait := l.buckets.allow("global:messages", l.messageRate*10, l.messageBurst*10); !ok {
					rateFail(w, wait)
					return
				}
				if ok, wait := l.buckets.allow(key, l.messageRate, l.messageBurst); !ok {
					rateFail(w, wait)
					return
				}
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/speech"):
				key = "speech:" + id
				if ok, wait := l.buckets.allow("global:speech", l.speechRate*10, l.speechBurst*10); !ok {
					rateFail(w, wait)
					return
				}
				if ok, wait := l.buckets.allow(key, l.speechRate, l.speechBurst); !ok {
					rateFail(w, wait)
					return
				}
			}
		}
		if ok, wait := l.buckets.allow(key, l.ipRate, l.ipBurst); !ok {
			rateFail(w, wait)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func rateFail(w http.ResponseWriter, wait time.Duration) {
	seconds := int(wait.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	fail(w, http.StatusTooManyRequests, "Rate limit exceeded")
}
