// Package llm talks to the language model that turns a campaign brief into a
// segment rule and message content.
//
// The model's output is never trusted: Generate parses and validates every
// answer, sends validation errors back for at most MaxRepairs repair rounds,
// and records every attempt for auditing.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

const (
	KindSegment = "segment"
	KindContent = "content"

	// MaxRepairs is how many times invalid output is sent back with its
	// validation errors before giving up.
	MaxRepairs = 2
)

// Message is one conversation turn; Role is "user" or "assistant".
type Message struct {
	Role    string
	Content string
}

// Request is one model call.
type Request struct {
	Kind          string
	PromptVersion string
	// Language is the recipients' language a content request writes for
	// ("tr", "en"); empty for segments.
	Language  string
	System    string
	Messages  []Message
	MaxTokens int

	// Input is the structured task input the prompt was built from
	// (SegmentInput or ContentInput). Real clients ignore it; the mock uses it
	// to answer deterministically.
	Input any
}

type Response struct {
	Text string
	// Model is the model that answered, if the client knows it; it can
	// differ from Client.Model() when a fallback model served the request.
	Model        string
	InputTokens  int
	OutputTokens int
}

// Client is a language model. Complete returns the full answer; if onDelta is
// not nil it also receives the text as it is generated.
type Client interface {
	Complete(ctx context.Context, req Request, onDelta func(text string)) (Response, error)
	Model() string
}

// Attempt is one model call as stored in llm_generations.
type Attempt struct {
	Kind             string
	Model            string
	PromptVersion    string
	Language         string // content only
	Attempt          int
	RawOutput        string
	Parsed           json.RawMessage // nil unless valid
	ValidationErrors json.RawMessage // nil unless invalid
	Valid            bool
	Error            string // transport or API error; no output
	InputTokens      int
	OutputTokens     int
	Latency          time.Duration
}

// Recorder stores attempts. Generate fails if an attempt cannot be recorded,
// because the audit trail is a requirement, not a best effort.
type Recorder interface {
	RecordAttempt(ctx context.Context, a Attempt) error
}

// GenerationError means the model did not produce valid output within the
// allowed repair rounds. Err is the last validation error.
type GenerationError struct {
	Kind     string
	Attempts int
	Err      error
}

func (e *GenerationError) Error() string {
	return fmt.Sprintf("the model could not produce a valid %s after %d attempts: %v", e.Kind, e.Attempts, e.Err)
}

func (e *GenerationError) Unwrap() error { return e.Err }

// CallError means the model call itself failed (unreachable, API error,
// refusal, truncation), as opposed to returning invalid output.
type CallError struct {
	Kind string
	Err  error
}

func (e *CallError) Error() string { return fmt.Sprintf("%s generation failed: %v", e.Kind, e.Err) }

func (e *CallError) Unwrap() error { return e.Err }

// Generate calls the model, parses its answer with parse, and on a validation
// error asks the model to repair it, at most MaxRepairs times. Every attempt is
// recorded. onDelta, if not nil, receives streamed text with the attempt
// number (starting at 1), so a UI can reset its preview when a repair starts.
//
// parse must return a segment.ValidationError or content.ValidationError for
// invalid output; any other error is treated the same way but stored as a
// single message.
func Generate[T any](ctx context.Context, c Client, rec Recorder, req Request,
	parse func([]byte) (T, error), onDelta func(attempt int, text string)) (T, error) {
	var zero T
	req.Messages = append([]Message(nil), req.Messages...) // repairs append to our copy

	// Attempts are recorded even if the caller gave up meanwhile, so the audit
	// trail matches what was actually spent.
	record := func(a Attempt) error { return rec.RecordAttempt(context.WithoutCancel(ctx), a) }

	var lastErr error
	for n := 1; n <= 1+MaxRepairs; n++ {
		var delta func(string)
		if onDelta != nil {
			delta = func(s string) { onDelta(n, s) }
		}

		start := time.Now()
		resp, err := c.Complete(ctx, req, delta)
		a := Attempt{
			Kind:          req.Kind,
			Model:         c.Model(),
			PromptVersion: req.PromptVersion,
			Language:      req.Language,
			Attempt:       n,
			RawOutput:     resp.Text,
			InputTokens:   resp.InputTokens,
			OutputTokens:  resp.OutputTokens,
			Latency:       time.Since(start),
		}
		if resp.Model != "" {
			a.Model = resp.Model
		}

		if err != nil {
			a.Error = err.Error()
			if rerr := record(a); rerr != nil {
				return zero, errors.Join(err, rerr)
			}
			return zero, &CallError{Kind: req.Kind, Err: err}
		}

		v, perr := parse([]byte(ExtractJSON(resp.Text)))
		if perr == nil {
			a.Valid = true
			if a.Parsed, err = json.Marshal(v); err != nil {
				return zero, fmt.Errorf("encode parsed %s: %w", req.Kind, err)
			}
			if err := record(a); err != nil {
				return zero, fmt.Errorf("record %s attempt: %w", req.Kind, err)
			}
			return v, nil
		}

		lastErr = perr
		a.ValidationErrors = problemsJSON(perr)
		if err := record(a); err != nil {
			return zero, fmt.Errorf("record %s attempt: %w", req.Kind, err)
		}
		req.Messages = append(req.Messages,
			Message{Role: "assistant", Content: resp.Text},
			Message{Role: "user", Content: RepairPrompt(perr)},
		)
	}
	return zero, &GenerationError{Kind: req.Kind, Attempts: 1 + MaxRepairs, Err: lastErr}
}

// ExtractJSON trims prose or Markdown code fences around a JSON object. If
// there is no object the text is returned as is, and parsing reports it.
func ExtractJSON(s string) string {
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i < 0 || j < i {
		return strings.TrimSpace(s)
	}
	return s[i : j+1]
}

// problem is the shape both segment.Problem and content.Problem share.
type problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func problemsOf(err error) []problem {
	var serr segment.ValidationError
	var cerr content.ValidationError
	var out []problem
	switch {
	case errors.As(err, &serr):
		for _, p := range serr {
			out = append(out, problem(p))
		}
	case errors.As(err, &cerr):
		for _, p := range cerr {
			out = append(out, problem(p))
		}
	default:
		out = []problem{{Message: err.Error()}}
	}
	return out
}

func problemsJSON(err error) json.RawMessage {
	b, _ := json.Marshal(problemsOf(err)) // cannot fail: strings only
	return b
}
