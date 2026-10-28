package mockprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// quiet is a provider that never misbehaves; tests turn on one behaviour each.
func quiet() Config {
	return Config{Burst: 1, RetryAfter: 2 * time.Second, Seed: 1}
}

type result struct {
	status int
	header http.Header
	body   map[string]any
}

func post(t *testing.T, h http.Handler, path, key string, req SendRequest) result {
	t.Helper()
	b, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(b)))
	if key != "" {
		r.Header.Set(IdempotencyHeader, key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return result{status: w.Code, header: w.Header(), body: body}
}

var (
	sms   = SendRequest{To: "+905321234567", Body: "Merhaba Ayşe, sepetindeki ürünler seni bekliyor!"}
	email = SendRequest{To: "ayse@example.com", Subject: "Sepetin seni bekliyor", Body: "<p>Merhaba</p>"}
)

func TestAcceptsValidMessages(t *testing.T) {
	s := New(quiet())
	h := s.Handler()

	for path, req := range map[string]SendRequest{"/sms": sms, "/email": email} {
		res := post(t, h, path, "key-"+path, req)
		if res.status != http.StatusAccepted || res.body["message_id"] == "" {
			t.Fatalf("%s: got %d %v, want 202 with message_id", path, res.status, res.body)
		}
	}
	st := s.Stats()
	if st[ChannelSMS].Accepted != 1 || st[ChannelEmail].Accepted != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		path string
		key  string
		req  SendRequest
	}{
		{"missing idempotency key", "/sms", "", sms},
		{"sms over 160 characters", "/sms", "k", SendRequest{To: sms.To, Body: strings.Repeat("a", 161)}},
		{"sms with subject", "/sms", "k", SendRequest{To: sms.To, Subject: "x", Body: "hi"}},
		{"bad phone", "/sms", "k", SendRequest{To: "05321234567", Body: "hi"}},
		{"empty body", "/sms", "k", SendRequest{To: sms.To, Body: "  "}},
		{"email subject over 80", "/email", "k", SendRequest{To: email.To, Subject: strings.Repeat("s", 81), Body: "hi"}},
		{"email without subject", "/email", "k", SendRequest{To: email.To, Body: "hi"}},
		{"bad email", "/email", "k", SendRequest{To: "not-an-email", Subject: "s", Body: "hi"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if res := post(t, New(quiet()).Handler(), tt.path, tt.key, tt.req); res.status != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", res.status)
			}
		})
	}
}

func TestSMSLimitCountsCharactersNotBytes(t *testing.T) {
	// 160 Turkish characters are 320 bytes but still within the limit.
	req := SendRequest{To: sms.To, Body: strings.Repeat("ş", 160)}
	if res := post(t, New(quiet()).Handler(), "/sms", "k", req); res.status != http.StatusAccepted {
		t.Fatalf("got %d, want 202", res.status)
	}
}

func TestIdempotentReplay(t *testing.T) {
	s := New(quiet())
	h := s.Handler()

	first := post(t, h, "/sms", "msg-1", sms)
	second := post(t, h, "/sms", "msg-1", sms)

	if second.status != http.StatusAccepted || second.header.Get(ReplayedHeader) != "true" {
		t.Fatalf("replay: got %d, replayed=%q", second.status, second.header.Get(ReplayedHeader))
	}
	if first.body["message_id"] != second.body["message_id"] {
		t.Fatalf("replay returned a different id: %v vs %v", first.body["message_id"], second.body["message_id"])
	}
	if st := s.Stats()[ChannelSMS]; st.Accepted != 1 || st.Replayed != 1 || st.DuplicateDeliveries != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestIdempotencyKeyConflict(t *testing.T) {
	h := New(quiet()).Handler()
	post(t, h, "/sms", "msg-1", sms)
	other := SendRequest{To: sms.To, Body: "different text"}
	if res := post(t, h, "/sms", "msg-1", other); res.status != http.StatusConflict {
		t.Fatalf("got %d, want 409", res.status)
	}
}

func TestCountsDuplicateDeliveries(t *testing.T) {
	s := New(quiet())
	h := s.Handler()
	post(t, h, "/sms", "msg-1", sms)
	post(t, h, "/sms", "msg-2", sms) // same person, same content, new key
	if st := s.Stats()[ChannelSMS]; st.DuplicateDeliveries != 1 {
		t.Fatalf("duplicate deliveries = %d, want 1", st.DuplicateDeliveries)
	}
}

func TestRateLimitPerChannel(t *testing.T) {
	cfg := quiet()
	cfg.RatePerSec, cfg.Burst = 0.001, 2
	s := New(cfg)
	h := s.Handler()

	for i, want := range []int{202, 202, 429} {
		res := post(t, h, "/sms", "sms-"+strconv.Itoa(i), SendRequest{To: sms.To, Body: "m" + strconv.Itoa(i)})
		if res.status != want {
			t.Fatalf("request %d: got %d, want %d", i, res.status, want)
		}
		if want == 429 && res.header.Get("Retry-After") != "2" {
			t.Fatalf("Retry-After = %q, want 2", res.header.Get("Retry-After"))
		}
	}
	// Email has its own bucket.
	if res := post(t, h, "/email", "email-a", email); res.status != http.StatusAccepted {
		t.Fatalf("email: got %d, want 202", res.status)
	}
	if st := s.Stats()[ChannelSMS]; st.RateLimited != 1 {
		t.Fatalf("rate limited = %d, want 1", st.RateLimited)
	}
}

func TestInjectedFailures(t *testing.T) {
	t.Run("throttle", func(t *testing.T) {
		cfg := quiet()
		cfg.ThrottleRate = 1
		res := post(t, New(cfg).Handler(), "/sms", "k", sms)
		if res.status != http.StatusTooManyRequests || res.header.Get("Retry-After") == "" {
			t.Fatalf("got %d Retry-After=%q, want 429 with Retry-After", res.status, res.header.Get("Retry-After"))
		}
	})
	t.Run("server error", func(t *testing.T) {
		cfg := quiet()
		cfg.ErrorRate = 1
		s := New(cfg)
		res := post(t, s.Handler(), "/sms", "k", sms)
		if res.status < 500 {
			t.Fatalf("got %d, want 5xx", res.status)
		}
		if st := s.Stats()[ChannelSMS]; st.Accepted != 0 {
			t.Fatal("an injected error must not accept the message")
		}
	})
}

// A lost response means the provider accepted the message but the client saw
// a 500. Retrying with the same key must not deliver it again.
func TestLostResponseThenRetryIsNotDeliveredTwice(t *testing.T) {
	cfg := quiet()
	cfg.LostResponseRate = 1
	s := New(cfg)
	h := s.Handler()

	if res := post(t, h, "/sms", "msg-1", sms); res.status != http.StatusInternalServerError {
		t.Fatalf("first attempt: got %d, want 500", res.status)
	}
	if res := post(t, h, "/sms", "msg-1", sms); res.status != http.StatusAccepted || res.header.Get(ReplayedHeader) != "true" {
		t.Fatalf("retry: got %d, want replayed 202", res.status)
	}
	if st := s.Stats()[ChannelSMS]; st.Accepted != 1 || st.DuplicateDeliveries != 0 || st.LostResponses != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestFailureRatesRoughlyMatchConfig(t *testing.T) {
	cfg := quiet()
	cfg.ErrorRate = 0.2
	s := New(cfg)
	h := s.Handler()

	const n = 2000
	for i := range n {
		post(t, h, "/sms", "k"+strconv.Itoa(i), SendRequest{To: sms.To, Body: "m" + strconv.Itoa(i)})
	}
	if got := float64(s.Stats()[ChannelSMS].InjectedErrors) / n; got < 0.16 || got > 0.24 {
		t.Fatalf("error share = %.3f, want about 0.20", got)
	}
}

func TestReset(t *testing.T) {
	s := New(quiet())
	h := s.Handler()
	post(t, h, "/sms", "msg-1", sms)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/reset", nil))
	if w.Code != http.StatusNoContent || s.Stats()[ChannelSMS].Accepted != 0 {
		t.Fatalf("reset: got %d, stats %+v", w.Code, s.Stats())
	}
}
