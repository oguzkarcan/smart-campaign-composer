package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

// fakeAnthropic is a stand-in for the Messages API. Each call takes the next
// handler; requests are kept for assertions.
type fakeAnthropic struct {
	mu       sync.Mutex
	handlers []http.HandlerFunc
	bodies   []map[string]any
	headers  []http.Header
}

func (f *fakeAnthropic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(b, &body)

	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.headers = append(f.headers, r.Header.Clone())
	if len(f.handlers) == 0 {
		f.mu.Unlock()
		http.Error(w, "no handler left", http.StatusTeapot)
		return
	}
	h := f.handlers[0]
	f.handlers = f.handlers[1:]
	f.mu.Unlock()
	h(w, r)
}

func newFakeClaude(t *testing.T, handlers ...http.HandlerFunc) (*Claude, *fakeAnthropic) {
	t.Helper()
	fake := &fakeAnthropic{handlers: handlers}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return NewClaude("test-key", "claude-sonnet-5-5", option.WithBaseURL(srv.URL), option.WithMaxRetries(1)), fake
}

// sse answers with a streamed message: an (empty) thinking block, then text
// split into the given chunks.
func sse(stopReason string, chunks ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		event := func(name, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data) }
		event("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":42,"output_tokens":1}}}`)
		event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`)
		event("content_block_stop", `{"type":"content_block_stop","index":0}`)
		event("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)
		for _, c := range chunks {
			b, _ := json.Marshal(c)
			event("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":`+string(b)+`}}`)
		}
		event("content_block_stop", `{"type":"content_block_stop","index":1}`)
		event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+stopReason+`","stop_sequence":null},"usage":{"output_tokens":17}}`)
		event("message_stop", `{"type":"message_stop"}`)
	}
}

func apiError(status int, typ string, header ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":"test error"}}`, typ)
	}
}

func TestClaudeStreams(t *testing.T) {
	c, fake := newFakeClaude(t, sse("end_turn", `{"match": `, `"all", "conditions": []}`))
	req := SegmentRequest(SegmentInput{Brief: "Everyone", Catalog: testCatalog})
	req.Messages = append(req.Messages, Message{Role: "assistant", Content: "{}"}, Message{Role: "user", Content: "fix it"})

	var deltas []string
	resp, err := c.Complete(context.Background(), req, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"match": "all", "conditions": []}` || len(deltas) != 2 {
		t.Fatalf("text = %q, deltas = %q", resp.Text, deltas)
	}
	if resp.InputTokens != 42 || resp.OutputTokens != 17 || resp.Model != "claude-sonnet-5-5" {
		t.Fatalf("response = %+v", resp)
	}

	body, h := fake.bodies[0], fake.headers[0]
	if h.Get("X-Api-Key") != "test-key" || !strings.Contains(h.Get("Anthropic-Beta"), "server-side-fallback-2026-07-01") {
		t.Errorf("headers: x-api-key %q, anthropic-beta %q", h.Get("X-Api-Key"), h.Get("Anthropic-Beta"))
	}
	if body["model"] != "claude-sonnet-5-5" || body["stream"] != true || body["max_tokens"] != float64(req.MaxTokens) ||
		body["fallbacks"] != "default" {
		t.Errorf("body = %v", body)
	}
	if effort := body["output_config"].(map[string]any)["effort"]; effort != "low" {
		t.Errorf("effort = %v, want low", effort)
	}
	for _, unwanted := range []string{"temperature", "top_p", "top_k", "thinking"} {
		if _, ok := body[unwanted]; ok {
			t.Errorf("body sets %q", unwanted)
		}
	}
	system := body["system"].([]any)[0].(map[string]any)["text"].(string)
	if system != req.System {
		t.Errorf("system prompt not sent as is")
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 3 || msgs[1].(map[string]any)["role"] != "assistant" || msgs[2].(map[string]any)["role"] != "user" {
		t.Errorf("messages = %v", msgs)
	}
}

func TestClaudeRefusalAndTruncation(t *testing.T) {
	for reason, want := range map[string]error{"refusal": ErrRefusal, "max_tokens": ErrTruncated} {
		c, _ := newFakeClaude(t, sse(reason, `{"match":`))
		_, err := c.Complete(context.Background(), SegmentRequest(SegmentInput{Brief: "x"}), nil)
		if !errors.Is(err, want) {
			t.Errorf("stop_reason %s: err = %v, want %v", reason, err, want)
		}
	}
}

// The SDK retries overload with retry-after; our code sees one success.
func TestClaudeRetriesOverload(t *testing.T) {
	c, fake := newFakeClaude(t, apiError(529, "overloaded_error", "Retry-After-Ms", "10"), sse("end_turn", "{}"))
	resp, err := c.Complete(context.Background(), SegmentRequest(SegmentInput{Brief: "x"}), nil)
	if err != nil || resp.Text != "{}" || len(fake.bodies) != 2 {
		t.Fatalf("resp = %+v, err = %v, calls = %d", resp, err, len(fake.bodies))
	}
}

func TestClaudeErrors(t *testing.T) {
	tests := []struct {
		handler http.HandlerFunc
		want    string
	}{
		{apiError(401, "authentication_error"), "check ANTHROPIC_API_KEY"},
		{apiError(400, "invalid_request_error"), "Claude API error 400"},
	}
	for _, tt := range tests {
		c, fake := newFakeClaude(t, tt.handler, tt.handler)
		_, err := c.Complete(context.Background(), SegmentRequest(SegmentInput{Brief: "x"}), nil)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("err = %v, want it to contain %q", err, tt.want)
		}
		if len(fake.bodies) != 1 {
			t.Errorf("%q: %d calls, client errors must not be retried", tt.want, len(fake.bodies))
		}
	}
}

// End to end: an invalid answer from Claude is repaired through the API, and
// the recorded attempts carry the real token usage.
func TestGenerateWithClaude(t *testing.T) {
	c, fake := newFakeClaude(t,
		sse("end_turn", `{"match": "every"}`),
		sse("end_turn", "```json\n"+validRule+"\n```"),
	)
	rec := &MemoryRecorder{}
	req := SegmentRequest(SegmentInput{Brief: "İstanbul", Catalog: testCatalog})
	if _, err := Generate(context.Background(), c, rec, req, parseRule, nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.Attempts) != 2 || rec.Attempts[0].Valid || !rec.Attempts[1].Valid || rec.Attempts[1].InputTokens != 42 {
		t.Fatalf("attempts = %+v", rec.Attempts)
	}
	repair := fake.bodies[1]["messages"].([]any)[2].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(repair, `match: must be "all" or "any"`) {
		t.Fatalf("repair prompt = %q", repair)
	}
}
