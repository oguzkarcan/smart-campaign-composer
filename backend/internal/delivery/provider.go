// Package delivery sends queued messages to the SMS/email provider. This
// file is the provider client: it renders a message for its recipient,
// sends it with the message id as idempotency key, and classifies the
// response as sent, retryable or permanently failed.
package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
)

const (
	IdempotencyHeader = "Idempotency-Key"

	// DefaultTimeout bounds one provider call. A timeout is retried; the
	// idempotency key makes that safe if the provider did accept it.
	DefaultTimeout = 10 * time.Second

	maxErrorBody = 4 << 10
)

// Outcome is what the worker should do with a message after a send.
type Outcome int

const (
	// Sent: the provider accepted the message.
	Sent Outcome = iota
	// Retry: a temporary failure (429, 5xx, 408, timeout, network error).
	Retry
	// Permanent: retrying cannot help (other 4xx, or a message that cannot
	// be rendered within the limits).
	Permanent
)

func (o Outcome) String() string {
	switch o {
	case Sent:
		return "sent"
	case Retry:
		return "retry"
	case Permanent:
		return "permanent"
	}
	return "unknown"
}

// Job is everything needed to send one message: the queued row, its
// recipient and the campaign's locked content templates. Messages are not
// stored rendered; the same Job always renders to the same payload, so a
// retry sends exactly what the first attempt sent.
type Job struct {
	MessageID  int64
	CampaignID int64
	Channel    string // content.ChannelSMS or content.ChannelEmail
	Language   string // the recipient's language at approval; Subject and Body are in it
	Attempt    int    // 1-based; the queue counts it when the job is claimed

	Email     string
	Phone     string
	FirstName string
	City      string

	Subject string // template, email only
	Body    string // template
}

// IdempotencyKey identifies the message at the provider across retries.
func (j Job) IdempotencyKey() string { return strconv.FormatInt(j.MessageID, 10) }

// SendRequest is the provider's request body for POST /sms and POST /email.
type SendRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

// Render fills the templates for the recipient and checks the channel limits
// on the result. Approval validated the worst case, but a recipient's data
// can change after approval; such a message fails here instead of being cut
// off or rejected by the provider.
func Render(j Job) (SendRequest, error) {
	vars := map[string]string{content.VarFirstName: j.FirstName, content.VarCity: j.City}
	req := SendRequest{Body: content.Render(j.Body, vars)}
	limits := map[string]string{}
	switch j.Channel {
	case content.ChannelSMS:
		req.To = j.Phone
		limits["sms.body"] = req.Body
	case content.ChannelEmail:
		req.To = j.Email
		req.Subject = content.Render(j.Subject, vars)
		limits["email.subject"] = req.Subject
		limits["email.body"] = req.Body
	default:
		return SendRequest{}, fmt.Errorf("unknown channel %q", j.Channel)
	}
	if req.To == "" {
		return SendRequest{}, fmt.Errorf("recipient has no %s address", j.Channel)
	}
	if strings.TrimSpace(req.Body) == "" {
		return SendRequest{}, errors.New("rendered body is empty")
	}
	if j.Channel == content.ChannelEmail {
		if strings.TrimSpace(req.Subject) == "" {
			return SendRequest{}, errors.New("rendered subject is empty")
		}
		// A line break in a subject could inject mail headers; recipient
		// data filled in after validation must not introduce one.
		if strings.ContainsAny(req.Subject, "\r\n") {
			return SendRequest{}, errors.New("rendered subject contains a line break")
		}
	}
	for path, s := range limits {
		if n := utf8.RuneCountInString(s); n > content.Limits[path] {
			return SendRequest{}, fmt.Errorf("rendered %s is %d characters, max %d", path, n, content.Limits[path])
		}
	}
	return req, nil
}

// Result describes one provider call. StatusCode is 0 when no response
// arrived (or the message was not sent because it could not be rendered).
type Result struct {
	Outcome           Outcome
	StatusCode        int
	ProviderMessageID string
	// RetryAfter is the provider's requested wait (Retry-After on 429/503),
	// 0 if it gave none. The worker waits at least this long.
	RetryAfter time.Duration
	Err        error // nil only when Sent
	Latency    time.Duration
}

// Client calls the provider's HTTP API.
type Client struct {
	baseURL string
	http    *http.Client
	now     func() time.Time
}

// NewClient returns a client for the provider at baseURL. Each call is
// bounded by timeout (DefaultTimeout if 0).
func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
		now:     time.Now,
	}
}

type sendResponse struct {
	MessageID string `json:"message_id"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Send renders and sends one message. It never returns a Go error; every
// failure is a Result with an Outcome, so the worker has one place to
// decide what happens next.
func (c *Client) Send(ctx context.Context, j Job) Result {
	req, err := Render(j)
	if err != nil {
		return Result{Outcome: Permanent, Err: err}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Result{Outcome: Permanent, Err: fmt.Errorf("encode request: %w", err)}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+j.Channel, bytes.NewReader(body))
	if err != nil {
		return Result{Outcome: Permanent, Err: fmt.Errorf("build request: %w", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(IdempotencyHeader, j.IdempotencyKey())

	start := time.Now()
	resp, err := c.http.Do(httpReq)
	if err != nil {
		// Timeouts and connection errors: the provider may or may not have
		// accepted the message; the idempotency key makes a retry safe.
		return Result{Outcome: Retry, Err: fmt.Errorf("provider call failed: %w", err), Latency: time.Since(start)}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	res := c.classify(resp, data)
	res.Latency = time.Since(start)
	return res
}

// classify maps a provider response to an Outcome: 2xx sent; 408, 429 and
// 5xx retryable; any other status permanent. A 409 (idempotency key reused
// with a different payload) is permanent: it means our rendering changed
// between attempts, and sending under a new key could deliver twice.
func (c *Client) classify(resp *http.Response, data []byte) Result {
	res := Result{StatusCode: resp.StatusCode}
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		var body sendResponse
		_ = json.Unmarshal(data, &body) // accepted even if the body is odd
		res.Outcome, res.ProviderMessageID = Sent, body.MessageID
		return res
	case code == http.StatusTooManyRequests, code == http.StatusServiceUnavailable:
		res.Outcome = Retry
		res.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
	case code == http.StatusRequestTimeout, code >= 500:
		res.Outcome = Retry
	default:
		res.Outcome = Permanent
	}
	res.Err = fmt.Errorf("provider returned %d: %s", resp.StatusCode, errorText(data))
	return res
}

// parseRetryAfter reads delay-seconds or an HTTP date. Missing, invalid or
// past values are 0 (use the normal backoff).
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

func errorText(data []byte) string {
	var body errorResponse
	if json.Unmarshal(data, &body) == nil && body.Error != "" {
		return body.Error
	}
	if s := strings.TrimSpace(string(data)); s != "" {
		return s
	}
	return "no details"
}
