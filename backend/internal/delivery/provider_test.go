package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/mockprovider"
)

var (
	smsJob = Job{
		MessageID: 42, Channel: "sms", Phone: "+905321234567", Email: "ayse@example.com",
		FirstName: "Ayşe", City: "İstanbul",
		Body: "{{first_name}}, {{city}} mağazasında %30 indirim!",
	}
	emailJob = Job{
		MessageID: 43, Channel: "email", Phone: "+905321234567", Email: "ayse@example.com",
		FirstName: "Ayşe", City: "İstanbul",
		Subject: "{{first_name}}, sepetin seni bekliyor", Body: "Merhaba {{first_name}}",
	}
)

func TestRender(t *testing.T) {
	req, err := Render(smsJob)
	if err != nil || req != (SendRequest{To: "+905321234567", Body: "Ayşe, İstanbul mağazasında %30 indirim!"}) {
		t.Fatalf("sms: %+v, err %v", req, err)
	}
	req, err = Render(emailJob)
	if err != nil || req != (SendRequest{To: "ayse@example.com", Subject: "Ayşe, sepetin seni bekliyor", Body: "Merhaba Ayşe"}) {
		t.Fatalf("email: %+v, err %v", req, err)
	}
}

// A recipient whose data changed after approval can push a message over the
// limit; it fails before reaching the provider.
func TestRenderEnforcesLimits(t *testing.T) {
	long := smsJob
	long.Body = strings.Repeat("a", 150) + "{{first_name}}"
	long.FirstName = "Muhammed Emin" // 150 + 13 = 163
	noPhone := smsJob
	noPhone.Phone = ""
	longSubject := emailJob
	longSubject.Subject = strings.Repeat("ş", 75) + " {{city}}" // 75 + 1 + 8 = 84 characters
	push := smsJob
	push.Channel = "push"
	blankSubject := emailJob
	blankSubject.Subject = " {{first_name}} "
	blankSubject.FirstName = "  "
	newlineSubject := emailJob
	newlineSubject.Subject = "{{city}} fırsatı"
	newlineSubject.City = "İstanbul\nBcc: x@example.com"
	crSubject := emailJob
	crSubject.Subject = "Merhaba\r{{first_name}}"

	for name, j := range map[string]Job{
		"long sms": long, "no phone": noPhone, "long subject": longSubject, "unknown channel": push,
		"blank subject": blankSubject, "newline from recipient data": newlineSubject, "carriage return": crSubject,
	} {
		if _, err := Render(j); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// Characters, not bytes: 160 Turkish letters fit.
	turkish := smsJob
	turkish.Body = strings.Repeat("ğ", 160)
	if _, err := Render(turkish); err != nil {
		t.Errorf("160 characters: %v", err)
	}
}

func TestSendRequest(t *testing.T) {
	var got struct {
		path, key, contentType string
		body                   SendRequest
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.key, got.contentType = r.URL.Path, r.Header.Get(IdempotencyHeader), r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"message_id": "email_00000001", "status": "accepted"}`))
	}))
	defer srv.Close()

	res := NewClient(srv.URL+"/", 0).Send(context.Background(), emailJob)
	if res.Outcome != Sent || res.Err != nil || res.StatusCode != 202 || res.ProviderMessageID != "email_00000001" {
		t.Fatalf("result = %+v", res)
	}
	if got.path != "/email" || got.key != "43" || got.contentType != "application/json" || got.body.Subject != "Ayşe, sepetin seni bekliyor" {
		t.Fatalf("request = %+v", got)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		status     int
		retryAfter string
		want       Outcome
		wantWait   time.Duration
	}{
		{http.StatusOK, "", Sent, 0},
		{http.StatusAccepted, "", Sent, 0},
		{http.StatusTooManyRequests, "3", Retry, 3 * time.Second},
		{http.StatusTooManyRequests, "", Retry, 0},
		{http.StatusTooManyRequests, "soon", Retry, 0},
		{http.StatusServiceUnavailable, "2", Retry, 2 * time.Second},
		{http.StatusInternalServerError, "", Retry, 0},
		{http.StatusBadGateway, "", Retry, 0},
		{http.StatusRequestTimeout, "", Retry, 0},
		{http.StatusBadRequest, "", Permanent, 0},
		{http.StatusUnauthorized, "", Permanent, 0},
		{http.StatusNotFound, "", Permanent, 0},
		{http.StatusConflict, "", Permanent, 0},
		{http.StatusUnprocessableEntity, "", Permanent, 0},
	}
	for _, tt := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tt.retryAfter != "" {
				w.Header().Set("Retry-After", tt.retryAfter)
			}
			w.WriteHeader(tt.status)
			_, _ = w.Write([]byte(`{"error": "nope"}`))
		}))
		res := NewClient(srv.URL, 0).Send(context.Background(), smsJob)
		srv.Close()
		if res.Outcome != tt.want || res.RetryAfter != tt.wantWait || res.StatusCode != tt.status {
			t.Errorf("%d Retry-After %q: %s wait %v, want %s wait %v", tt.status, tt.retryAfter, res.Outcome, res.RetryAfter, tt.want, tt.wantWait)
		}
		if (res.Err == nil) != (tt.want == Sent) {
			t.Errorf("%d: err = %v", tt.status, res.Err)
		}
		if tt.want != Sent && !strings.Contains(res.Err.Error(), "nope") {
			t.Errorf("%d: error %q does not carry the provider's message", tt.status, res.Err)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	tests := map[string]time.Duration{
		"":                              0,
		"0":                             0,
		"-5":                            0,
		"7":                             7 * time.Second,
		" 2 ":                           2 * time.Second,
		"Sat, 31 Oct 2026 12:00:30 GMT": 30 * time.Second,
		"Sat, 31 Oct 2026 11:59:00 GMT": 0, // in the past
		"tomorrow":                      0,
	}
	for in, want := range tests {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTimeoutAndNetworkErrorsAreRetried(t *testing.T) {
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(block)

	res := NewClient(slow.URL, 50*time.Millisecond).Send(context.Background(), smsJob)
	if res.Outcome != Retry || res.StatusCode != 0 || res.Err == nil || res.Latency < 50*time.Millisecond {
		t.Fatalf("timeout: %+v", res)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if res := NewClient(closed.URL, 0).Send(context.Background(), smsJob); res.Outcome != Retry || res.Err == nil {
		t.Fatalf("connection refused: %+v", res)
	}
}

// Unsendable messages are not sent at all.
func TestSendDoesNotCallProviderForInvalidMessages(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true }))
	defer srv.Close()

	j := smsJob
	j.Body = strings.Repeat("a", 161)
	res := NewClient(srv.URL, 0).Send(context.Background(), j)
	if res.Outcome != Permanent || res.Err == nil || called {
		t.Fatalf("result %+v, provider called: %v", res, called)
	}
}

// Against the real mock provider: a lost response (accepted, but the client
// sees a 500) is retried with the same key and replayed, not delivered twice.
func TestLostResponseIsReplayedNotDuplicated(t *testing.T) {
	p := mockprovider.New(mockprovider.Config{Burst: 10, LostResponseRate: 1, Seed: 1})
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()
	c := NewClient(srv.URL, 0)

	first := c.Send(context.Background(), smsJob)
	if first.Outcome != Retry || first.StatusCode != 500 {
		t.Fatalf("first attempt: %+v", first)
	}
	second := c.Send(context.Background(), smsJob)
	if second.Outcome != Sent || second.ProviderMessageID == "" {
		t.Fatalf("retry: %+v", second)
	}
	st := p.Stats()["sms"]
	if st.Accepted != 1 || st.Replayed != 1 || st.DuplicateDeliveries != 0 {
		t.Fatalf("provider stats = %+v", st)
	}
}

func TestRateLimitHonoursRetryAfterFromMockProvider(t *testing.T) {
	p := mockprovider.New(mockprovider.Config{RatePerSec: 1, Burst: 1, RetryAfter: 2 * time.Second, Seed: 1})
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()
	c := NewClient(srv.URL, 0)

	if res := c.Send(context.Background(), smsJob); res.Outcome != Sent {
		t.Fatalf("first: %+v", res)
	}
	other := smsJob
	other.MessageID = 99
	res := c.Send(context.Background(), other)
	if res.Outcome != Retry || res.StatusCode != 429 || res.RetryAfter != 2*time.Second {
		t.Fatalf("over the limit: %+v", res)
	}
}

func TestCancelledContextIsRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := NewClient(srv.URL, 0).Send(ctx, smsJob)
	if res.Outcome != Retry || !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("result = %+v", res)
	}
}
