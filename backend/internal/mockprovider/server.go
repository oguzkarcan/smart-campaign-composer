// Package mockprovider imitates an SMS/email delivery provider. Nothing is
// ever delivered; it only records what it accepted. It injects latency,
// errors, 429s and lost responses so the worker's retry, rate limiting and
// idempotency logic can be exercised for real.
package mockprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/time/rate"
)

const (
	ChannelSMS   = "sms"
	ChannelEmail = "email"

	IdempotencyHeader = "Idempotency-Key"
	ReplayedHeader    = "Idempotent-Replayed"

	maxSMSLength     = 160
	maxSubjectLength = 80
	maxBodyBytes     = 64 << 10
)

var (
	phonePattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// SendRequest is the body of POST /sms and POST /email. Subject is email-only.
type SendRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

type SendResponse struct {
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
}

// ChannelStats counts outcomes per channel. Accepted counts unique
// idempotency keys; DuplicateDeliveries counts identical recipient+content
// accepted under a different key, which the sender must never cause.
type ChannelStats struct {
	Accepted            int64 `json:"accepted"`
	Replayed            int64 `json:"replayed"`
	DuplicateDeliveries int64 `json:"duplicate_deliveries"`
	RateLimited         int64 `json:"rate_limited"`
	InjectedThrottles   int64 `json:"injected_throttles"`
	InjectedErrors      int64 `json:"injected_errors"`
	LostResponses       int64 `json:"lost_responses"`
	Rejected            int64 `json:"rejected"`
	Conflicts           int64 `json:"conflicts"`
}

type record struct {
	messageID   string
	payloadHash string
}

type Server struct {
	cfg Config

	mu        sync.Mutex
	rng       *rand.Rand
	limiters  map[string]*rate.Limiter
	byKey     map[string]record // idempotency key -> accepted message
	delivered map[string]bool   // payload hash -> already accepted once
	stats     map[string]*ChannelStats
	seq       int64
}

func New(cfg Config) *Server {
	s := &Server{cfg: cfg}
	s.reset()
	return s
}

func (s *Server) reset() {
	s.rng = rand.New(rand.NewPCG(s.cfg.Seed, 0x9e37))
	s.limiters = map[string]*rate.Limiter{}
	s.byKey = map[string]record{}
	s.delivered = map[string]bool{}
	s.stats = map[string]*ChannelStats{ChannelSMS: {}, ChannelEmail: {}}
	s.seq = 0
	for _, ch := range []string{ChannelSMS, ChannelEmail} {
		limit := rate.Inf
		if s.cfg.RatePerSec > 0 {
			limit = rate.Limit(s.cfg.RatePerSec)
		}
		s.limiters[ch] = rate.NewLimiter(limit, s.cfg.Burst)
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sms", s.send(ChannelSMS))
	mux.HandleFunc("POST /email", s.send(ChannelEmail))
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("POST /reset", s.handleReset)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

// Stats returns a copy of the per-channel counters.
func (s *Server) Stats() map[string]ChannelStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ChannelStats, len(s.stats))
	for ch, st := range s.stats {
		out[ch] = *st
	}
	return out
}

type outcome int

const (
	outcomeAccept outcome = iota
	outcomeThrottle
	outcomeError
	outcomeLostResponse
)

func (s *Server) send(channel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req SendRequest
		if err := decode(w, r, &req); err != nil {
			s.reject(w, channel, err.Error())
			return
		}
		if msg := validate(channel, req); msg != "" {
			s.reject(w, channel, msg)
			return
		}
		key := strings.TrimSpace(r.Header.Get(IdempotencyHeader))
		if key == "" {
			s.reject(w, channel, IdempotencyHeader+" header is required")
			return
		}

		if err := sleep(r.Context(), s.latency()); err != nil {
			return // client gave up
		}

		hash := payloadHash(channel, req)
		s.mu.Lock()
		defer s.mu.Unlock()
		st := s.stats[channel]

		// Replays are answered before rate limiting and failure injection,
		// like real providers that cache the first response.
		if rec, ok := s.byKey[key]; ok {
			if rec.payloadHash != hash {
				st.Conflicts++
				writeError(w, http.StatusConflict, "idempotency key reused with a different payload")
				return
			}
			st.Replayed++
			w.Header().Set(ReplayedHeader, "true")
			writeJSON(w, http.StatusAccepted, SendResponse{MessageID: rec.messageID, Status: "accepted"})
			return
		}

		if !s.limiters[channel].Allow() {
			st.RateLimited++
			s.tooManyRequests(w)
			return
		}

		switch s.roll() {
		case outcomeThrottle:
			st.InjectedThrottles++
			s.tooManyRequests(w)
		case outcomeError:
			st.InjectedErrors++
			status := http.StatusInternalServerError
			if s.rng.IntN(2) == 0 {
				status = http.StatusServiceUnavailable
			}
			writeError(w, status, "injected provider error")
		case outcomeLostResponse:
			st.LostResponses++
			s.accept(channel, key, hash)
			writeError(w, http.StatusInternalServerError, "injected error after accepting the message")
		default:
			id := s.accept(channel, key, hash)
			writeJSON(w, http.StatusAccepted, SendResponse{MessageID: id, Status: "accepted"})
		}
	}
}

// accept records a message; the caller holds s.mu.
func (s *Server) accept(channel, key, hash string) string {
	s.seq++
	id := fmt.Sprintf("%s_%08d", channel, s.seq)
	s.byKey[key] = record{messageID: id, payloadHash: hash}
	st := s.stats[channel]
	st.Accepted++
	if s.delivered[hash] {
		st.DuplicateDeliveries++
	}
	s.delivered[hash] = true
	return id
}

// roll picks a random outcome; the caller holds s.mu.
func (s *Server) roll() outcome {
	p := s.rng.Float64()
	switch {
	case p < s.cfg.ThrottleRate:
		return outcomeThrottle
	case p < s.cfg.ThrottleRate+s.cfg.ErrorRate:
		return outcomeError
	case p < s.cfg.ThrottleRate+s.cfg.ErrorRate+s.cfg.LostResponseRate:
		return outcomeLostResponse
	default:
		return outcomeAccept
	}
}

func (s *Server) latency() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.SlowRate > 0 && s.rng.Float64() < s.cfg.SlowRate {
		return s.cfg.SlowLatency
	}
	spread := s.cfg.MaxLatency - s.cfg.MinLatency
	if spread <= 0 {
		return s.cfg.MinLatency
	}
	return s.cfg.MinLatency + time.Duration(s.rng.Int64N(int64(spread)))
}

func (s *Server) tooManyRequests(w http.ResponseWriter) {
	secs := int(math.Ceil(s.cfg.RetryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
}

func (s *Server) reject(w http.ResponseWriter, channel, msg string) {
	s.mu.Lock()
	s.stats[channel].Rejected++
	s.mu.Unlock()
	writeError(w, http.StatusBadRequest, msg)
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Stats())
}

func (s *Server) handleReset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.reset()
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func validate(channel string, req SendRequest) string {
	if strings.TrimSpace(req.Body) == "" {
		return "body is required"
	}
	switch channel {
	case ChannelSMS:
		if !phonePattern.MatchString(req.To) {
			return "to must be an E.164 phone number"
		}
		if req.Subject != "" {
			return "sms has no subject"
		}
		if n := utf8.RuneCountInString(req.Body); n > maxSMSLength {
			return fmt.Sprintf("sms body is %d characters, max %d", n, maxSMSLength)
		}
	case ChannelEmail:
		if !emailPattern.MatchString(req.To) {
			return "to must be an email address"
		}
		n := utf8.RuneCountInString(req.Subject)
		if n == 0 || n > maxSubjectLength {
			return fmt.Sprintf("subject must be 1-%d characters, got %d", maxSubjectLength, n)
		}
	}
	return ""
}

func payloadHash(channel string, req SendRequest) string {
	sum := sha256.Sum256([]byte(channel + "\x00" + req.To + "\x00" + req.Subject + "\x00" + req.Body))
	return hex.EncodeToString(sum[:])
}

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
