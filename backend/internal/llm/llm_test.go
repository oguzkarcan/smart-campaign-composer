package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

var testCatalog = segment.Catalog{
	"city":     {"Ankara", "İstanbul", "İzmir"},
	"language": {"en", "tr"},
}

const validRule = `{"match": "all", "conditions": [{"field": "city", "op": "eq", "value": "İstanbul"}]}`

func parseRule(b []byte) (segment.Rule, error) { return segment.Parse(b, testCatalog) }

func generateRule(t *testing.T, answers ...ScriptedAnswer) (segment.Rule, error, *Scripted, *MemoryRecorder) {
	t.Helper()
	client, rec := NewScripted(answers...), &MemoryRecorder{}
	req := SegmentRequest(SegmentInput{Brief: "İstanbul users", Catalog: testCatalog})
	r, err := Generate(context.Background(), client, rec, req, parseRule, nil)
	return r, err, client, rec
}

func TestGenerateValidFirstTime(t *testing.T) {
	r, err, client, rec := generateRule(t, ScriptedAnswer{Text: validRule})
	if err != nil {
		t.Fatal(err)
	}
	if r.Conditions[0].Value[0] != "İstanbul" {
		t.Fatalf("rule = %+v", r)
	}
	if len(client.Requests) != 1 || len(rec.Attempts) != 1 {
		t.Fatalf("calls = %d, attempts = %d, want 1 and 1", len(client.Requests), len(rec.Attempts))
	}
	a := rec.Attempts[0]
	if !a.Valid || a.Attempt != 1 || a.Kind != KindSegment || a.PromptVersion != SegmentPromptVersion ||
		a.Model != "scripted" || a.RawOutput != validRule || a.Parsed == nil || a.ValidationErrors != nil {
		t.Fatalf("attempt = %+v", a)
	}
}

func TestGenerateRepairs(t *testing.T) {
	invalid := `{"match": "all", "conditions": [{"field": "country", "op": "eq", "value": "TR"}]}`
	r, err, client, rec := generateRule(t, ScriptedAnswer{Text: invalid}, ScriptedAnswer{Text: validRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Conditions) != 1 || len(rec.Attempts) != 2 {
		t.Fatalf("rule = %+v, attempts = %d", r, len(rec.Attempts))
	}

	first := rec.Attempts[0]
	if first.Valid || first.Parsed != nil || !strings.Contains(string(first.ValidationErrors), `"path":"conditions[0].field"`) {
		t.Fatalf("first attempt = %+v, validation errors %s", first, first.ValidationErrors)
	}
	if !rec.Attempts[1].Valid || rec.Attempts[1].Attempt != 2 {
		t.Fatalf("second attempt = %+v", rec.Attempts[1])
	}

	// The repair call replays the bad answer and lists what was wrong.
	msgs := client.Requests[1].Messages
	if len(msgs) != 3 || msgs[1].Role != "assistant" || msgs[1].Content != invalid || msgs[2].Role != "user" ||
		!strings.Contains(msgs[2].Content, `conditions[0].field: unknown field "country"`) {
		t.Fatalf("repair messages = %+v", msgs)
	}
	if len(client.Requests[0].Messages) != 1 {
		t.Fatal("the first request was modified by the repair")
	}
}

func TestGenerateGivesUpAfterMaxRepairs(t *testing.T) {
	bad := ScriptedAnswer{Text: `{"match": "all", "conditions": [], "sql": "DROP TABLE users"}`}
	_, err, client, rec := generateRule(t, bad, bad, bad, ScriptedAnswer{Text: validRule})

	var gerr *GenerationError
	if !errors.As(err, &gerr) || gerr.Attempts != 1+MaxRepairs || gerr.Kind != KindSegment {
		t.Fatalf("err = %v, want GenerationError after %d attempts", err, 1+MaxRepairs)
	}
	var verr segment.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want it to wrap the last validation error", err)
	}
	if len(client.Requests) != 1+MaxRepairs || len(rec.Attempts) != 1+MaxRepairs {
		t.Fatalf("calls = %d, attempts = %d, want %d", len(client.Requests), len(rec.Attempts), 1+MaxRepairs)
	}
	for i, a := range rec.Attempts {
		if a.Valid || a.Attempt != i+1 || a.ValidationErrors == nil {
			t.Fatalf("attempt %d = %+v", i, a)
		}
	}
}

func TestGenerateTransportErrorIsNotRepaired(t *testing.T) {
	_, err, client, rec := generateRule(t, ScriptedAnswer{Err: errors.New("overloaded")}, ScriptedAnswer{Text: validRule})
	if err == nil || !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("err = %v", err)
	}
	var gerr *GenerationError
	var cerr *CallError
	if errors.As(err, &gerr) || !errors.As(err, &cerr) || cerr.Kind != KindSegment {
		t.Fatalf("err = %#v, want a CallError, not a GenerationError", err)
	}
	if len(client.Requests) != 1 || len(rec.Attempts) != 1 || rec.Attempts[0].Error != "overloaded" || rec.Attempts[0].Valid {
		t.Fatalf("calls = %d, attempts = %+v", len(client.Requests), rec.Attempts)
	}
}

type failingRecorder struct{}

func (failingRecorder) RecordAttempt(context.Context, Attempt) error { return errors.New("db down") }

func TestGenerateFailsIfAttemptCannotBeRecorded(t *testing.T) {
	req := SegmentRequest(SegmentInput{Brief: "x", Catalog: testCatalog})
	_, err := Generate(context.Background(), NewScripted(ScriptedAnswer{Text: validRule}), failingRecorder{}, req, parseRule, nil)
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("err = %v, want the recording error", err)
	}
}

func TestGenerateStripsCodeFences(t *testing.T) {
	if _, err, _, _ := generateRule(t, ScriptedAnswer{Text: "Here it is:\n```json\n" + validRule + "\n```"}); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateReportsAttemptOnDelta(t *testing.T) {
	client := NewScripted(ScriptedAnswer{Text: `{}`}, ScriptedAnswer{Text: validRule})
	var seen []int
	req := SegmentRequest(SegmentInput{Brief: "x", Catalog: testCatalog})
	_, err := Generate(context.Background(), client, &MemoryRecorder{}, req, parseRule,
		func(attempt int, _ string) { seen = append(seen, attempt) })
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Fatalf("delta attempts = %v, want [1 2]", seen)
	}
}

func TestGenerateContentRepairsLength(t *testing.T) {
	long := `{"sms": {"body": "` + strings.Repeat("a", 150) + ` {{first_name}}"}}`
	short := `{"sms": {"body": "{{first_name}}, your cart is waiting!"}}`
	in := ContentInput{Brief: "x", Channels: []string{content.ChannelSMS}, Lengths: content.MaxLengths{"first_name": 13}}
	client, rec := NewScripted(ScriptedAnswer{Text: long}, ScriptedAnswer{Text: short}), &MemoryRecorder{}
	parse := func(b []byte) (content.Content, error) { return content.Parse(b, in.Channels, in.Lengths) }

	c, err := Generate(context.Background(), client, rec, ContentRequest(in), parse, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.SMS == nil || c.SMS.Body != "{{first_name}}, your cart is waiting!" {
		t.Fatalf("content = %+v", c)
	}
	repair := client.Requests[1].Messages[2].Content
	if !strings.Contains(repair, "sms.body: is 165 characters in the worst case, max 160; shorten it by at least 5") {
		t.Fatalf("repair prompt = %q", repair)
	}
	var problems []problem
	if err := json.Unmarshal(rec.Attempts[0].ValidationErrors, &problems); err != nil || len(problems) != 1 || problems[0].Path != "sms.body" {
		t.Fatalf("stored validation errors = %s (%v)", rec.Attempts[0].ValidationErrors, err)
	}
}

func TestExtractJSON(t *testing.T) {
	tests := map[string]string{
		`{"a": 1}`:                 `{"a": 1}`,
		"```json\n{\"a\": 1}\n```": `{"a": 1}`,
		"Sure! {\"a\": {}} Done.":  `{"a": {}}`,
		"  no json here  ":         "no json here",
		"} backwards {":            "} backwards {",
	}
	for in, want := range tests {
		if got := ExtractJSON(in); got != want {
			t.Errorf("ExtractJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

// ExtractJSON keeps everything up to the last "}", so an answer with a stray
// closing brace must be caught by the parser and repaired.
func TestGenerateRepairsTrailingBrace(t *testing.T) {
	_, err, client, rec := generateRule(t, ScriptedAnswer{Text: validRule + "}"}, ScriptedAnswer{Text: validRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Attempts) != 2 || rec.Attempts[0].Valid || !strings.Contains(client.Requests[1].Messages[2].Content, "unexpected data") {
		t.Fatalf("attempts = %+v", rec.Attempts)
	}
}
