package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

var (
	// ErrRefusal means Claude declined the request (and so did the fallback).
	ErrRefusal = errors.New("Claude declined to answer this brief")
	// ErrTruncated means the answer hit max_tokens. It is not repaired: a
	// repair with the same limit would most likely be cut off again.
	ErrTruncated = errors.New("Claude's answer was cut off at the token limit")
)

// Claude calls the Anthropic Messages API through the official SDK, which
// also retries rate limits (429), overload (529), 5xx and connection errors
// with backoff, honouring retry-after.
type Claude struct {
	client anthropic.Client
	model  string
}

// NewClaude creates a client for model. opts are passed to the SDK, e.g. to
// point it at a test server or change retries.
func NewClaude(apiKey, model string, opts ...option.RequestOption) *Claude {
	opts = append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	return &Claude{client: anthropic.NewClient(opts...), model: model}
}

func (c *Claude) Model() string { return c.model }

// Complete always streams: it keeps the connection busy on long answers and
// lets content appear in the UI as it is written.
func (c *Claude) Complete(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	msgs := make([]anthropic.BetaMessageParam, len(req.Messages))
	for i, m := range req.Messages {
		role := anthropic.BetaMessageParamRoleUser
		if m.Role == "assistant" {
			role = anthropic.BetaMessageParamRoleAssistant
		}
		msgs[i] = anthropic.BetaMessageParam{Role: role, Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(m.Content)}}
	}
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(req.MaxTokens),
		System:    []anthropic.BetaTextBlockParam{{Text: req.System}},
		Messages:  msgs,
		// Thinking stays adaptive (the default); these are short structured
		// tasks, so low effort keeps latency and cost down.
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortLow},
		// A request declined by a safety classifier is re-served by a fallback
		// model chosen by refusal category, within the same call.
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	}

	stream := c.client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	var msg anthropic.BetaMessage
	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			return Response{}, fmt.Errorf("claude stream: %w", err)
		}
		if onDelta == nil {
			continue
		}
		if e, ok := event.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
			if d, ok := e.Delta.AsAny().(anthropic.BetaTextDelta); ok {
				onDelta(d.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return Response{}, describe(err)
	}

	var text strings.Builder
	for _, block := range msg.Content {
		if b, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	resp := Response{
		Text:         text.String(),
		Model:        string(msg.Model),
		InputTokens:  int(msg.Usage.InputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return resp, ErrRefusal
	case anthropic.BetaStopReasonMaxTokens:
		return resp, ErrTruncated
	}
	return resp, nil
}

// describe turns SDK errors into messages a marketer can act on; the
// original error is kept for logs and errors.As.
func describe(err error) error {
	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		return fmt.Errorf("could not reach Claude: %w", err)
	}
	switch apierr.StatusCode {
	case 401, 403:
		return fmt.Errorf("Claude rejected the API key (check ANTHROPIC_API_KEY): %w", err)
	case 429, 529:
		return fmt.Errorf("Claude is busy, try again in a minute: %w", err)
	default:
		return fmt.Errorf("Claude API error %d: %w", apierr.StatusCode, err)
	}
}
