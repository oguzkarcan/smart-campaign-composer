package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign/campaigntest"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
)

const exampleBrief = "Son 30 günde sepete ürün ekleyip satın almayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder"

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil))) // keep test output readable
	os.Exit(m.Run())
}

func newTestAPI(client llm.Client) (http.Handler, *campaigntest.MemStore) {
	store := campaigntest.NewMemStore()
	return New(fakePinger{}, campaign.NewService(store, client)).Handler(), store
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func decodeBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func create(t *testing.T, h http.Handler, channels string) campaign.Campaign {
	t.Helper()
	w := do(t, h, "POST", "/campaigns", `{"brief": "`+exampleBrief+`", "channels": `+channels+`}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	return decodeBody[campaign.Campaign](t, w)
}

type event struct {
	name string
	data string
}

func parseSSE(t *testing.T, body string) []event {
	t.Helper()
	var out []event
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var e event
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			default:
				t.Fatalf("unexpected SSE line %q", line)
			}
		}
		out = append(out, e)
	}
	return out
}

func TestCreateCampaign(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	w := do(t, h, "POST", "/campaigns", `{"brief": "`+exampleBrief+`", "channels": ["email", "sms"]}`)
	if w.Code != http.StatusCreated || w.Header().Get("Location") == "" {
		t.Fatalf("status %d, location %q: %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	c := decodeBody[campaign.Campaign](t, w)
	if c.Status != campaign.StatusAudienceReady || c.Audience == nil || c.Rule == nil ||
		c.VariableLengths["first_name"] == 0 || c.Limits["sms.body"] != 160 {
		t.Fatalf("campaign = %+v", c)
	}

	got := do(t, h, "GET", "/campaigns/1", "")
	if got.Code != http.StatusOK || decodeBody[campaign.Campaign](t, got).ID != c.ID {
		t.Fatalf("get: %d %s", got.Code, got.Body)
	}
}

func TestCreateCampaignRejectsBadInput(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	for _, body := range []string{
		`{"brief": "x", "channels": ["fax"]}`,
		`{"brief": "", "channels": ["sms"]}`,
		`{"brief": "x", "channels": ["sms"], "segment_rule": {}}`, // the rule is never accepted from clients
		`not json`,
	} {
		w := do(t, h, "POST", "/campaigns", body)
		if w.Code != http.StatusBadRequest || decodeBody[errorResponse](t, w).Error == "" {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
}

// A brief the model cannot turn into a valid rule still creates a draft the
// marketer can retry, with the reason attached.
func TestCreateCampaignKeepsDraftWhenSegmentFails(t *testing.T) {
	bad := llm.ScriptedAnswer{Text: `{"match": "all", "conditions": [{"field": "income", "op": "eq", "value": "x"}]}`}
	h, _ := newTestAPI(llm.NewScripted(bad, bad, bad))
	w := do(t, h, "POST", "/campaigns", `{"brief": "high income users", "channels": ["sms"]}`)
	c := decodeBody[campaign.Campaign](t, w)
	if w.Code != http.StatusCreated || c.Status != campaign.StatusDraft || !strings.Contains(c.LastError, "income") {
		t.Fatalf("%d: %+v", w.Code, c)
	}
}

func TestGenerateSegmentErrors(t *testing.T) {
	bad := llm.ScriptedAnswer{Text: `{"match": "every"}`}
	tests := []struct {
		name    string
		answers []llm.ScriptedAnswer
		status  int
	}{
		{"invalid after repairs", []llm.ScriptedAnswer{bad, bad, bad}, http.StatusUnprocessableEntity},
		{"model unreachable", []llm.ScriptedAnswer{{Err: errors.New("connection refused")}}, http.StatusBadGateway},
		{"refusal", []llm.ScriptedAnswer{{Err: llm.ErrRefusal}}, http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The first answer serves the create request.
			answers := append([]llm.ScriptedAnswer{{Text: `{"match": "all", "conditions": []}`}}, tt.answers...)
			h, _ := newTestAPI(llm.NewScripted(answers...))
			c := create(t, h, `["sms"]`)
			w := do(t, h, "POST", "/campaigns/1/segment", "")
			body := decodeBody[errorResponse](t, w)
			if w.Code != tt.status || body.Error == "" || c.ID != 1 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if tt.status == http.StatusUnprocessableEntity && tt.name == "invalid after repairs" &&
				(len(body.Problems) == 0 || body.Problems[0].Path != "match") {
				t.Fatalf("problems = %+v", body.Problems)
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	for _, path := range []string{"/campaigns/99", "/campaigns/abc", "/campaigns/0"} {
		if w := do(t, h, "GET", path, ""); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d", path, w.Code)
		}
	}
	if w := do(t, h, "POST", "/campaigns/99/content", ""); w.Code != http.StatusNotFound ||
		!strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("content for a missing campaign: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestGenerateContentStreams(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	create(t, h, `["sms", "email"]`)

	w := do(t, h, "POST", "/campaigns/1/content", "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %s: %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	events := parseSSE(t, w.Body.String())
	last := events[len(events)-1]
	if len(events) < 3 || last.name != "done" {
		t.Fatalf("events = %+v", events)
	}

	var streamed strings.Builder
	for _, e := range events[:len(events)-1] {
		var d deltaEvent
		if e.name != "delta" || json.Unmarshal([]byte(e.data), &d) != nil || d.Attempt != 1 {
			t.Fatalf("event %+v", e)
		}
		streamed.WriteString(d.Text)
	}
	var c campaign.Campaign
	if err := json.Unmarshal([]byte(last.data), &c); err != nil {
		t.Fatal(err)
	}
	if c.Status != campaign.StatusContentReady || len(c.Contents) != 2 || !strings.Contains(streamed.String(), c.Contents[0].Body) {
		t.Fatalf("done campaign = %+v", c)
	}
}

func TestGenerateContentConflictIsPlainJSON(t *testing.T) {
	bad := llm.ScriptedAnswer{Text: `{"match": "every"}`}
	h, _ := newTestAPI(llm.NewScripted(bad, bad, bad)) // campaign stays a draft
	create(t, h, `["sms"]`)
	w := do(t, h, "POST", "/campaigns/1/content", "")
	if w.Code != http.StatusConflict || decodeBody[errorResponse](t, w).Error == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestGenerateContentStreamsRepairsAndFailure(t *testing.T) {
	tooLong := llm.ScriptedAnswer{Text: `{"sms": {"body": "` + strings.Repeat("a", 170) + `"}}`}
	h, _ := newTestAPI(llm.NewScripted(llm.ScriptedAnswer{Text: `{"match": "all", "conditions": []}`}, tooLong, tooLong, tooLong))
	create(t, h, `["sms"]`)

	w := do(t, h, "POST", "/campaigns/1/content", "")
	events := parseSSE(t, w.Body.String())
	if len(events) != 4 {
		t.Fatalf("events = %+v", events)
	}
	for i, e := range events[:3] {
		var d deltaEvent
		if err := json.Unmarshal([]byte(e.data), &d); err != nil || e.name != "delta" || d.Attempt != i+1 {
			t.Fatalf("event %d = %+v, want delta of attempt %d", i, e, i+1)
		}
	}
	var body errorResponse
	if err := json.Unmarshal([]byte(events[3].data), &body); err != nil || events[3].name != "error" ||
		body.Status != http.StatusUnprocessableEntity || len(body.Problems) != 1 || body.Problems[0].Path != "sms.body" {
		t.Fatalf("error event = %+v", events[3])
	}
}

func TestEditContent(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	create(t, h, `["sms", "email"]`)
	do(t, h, "POST", "/campaigns/1/content", "")

	w := do(t, h, "PUT", "/campaigns/1/content/sms", `{"body": "{{ first_name }}, son gün!"}`)
	c := decodeBody[campaign.Campaign](t, w)
	if w.Code != http.StatusOK || c.Contents[0].Body != "{{first_name}}, son gün!" || !c.Contents[0].HumanEdited {
		t.Fatalf("%d %+v", w.Code, c.Contents)
	}

	tests := []struct {
		path, body string
		status     int
	}{
		{"/campaigns/1/content/sms", `{"body": "` + strings.Repeat("a", 161) + `"}`, http.StatusUnprocessableEntity},
		{"/campaigns/1/content/sms", `{"body": "Hi {{phone}}"}`, http.StatusUnprocessableEntity},
		{"/campaigns/1/content/email", `{"subject": "", "body": "b"}`, http.StatusUnprocessableEntity},
		{"/campaigns/1/content/push", `{"body": "Hi"}`, http.StatusBadRequest},
		{"/campaigns/1/content/sms", `{"body": "Hi", "human_edited": false}`, http.StatusBadRequest},
		{"/campaigns/9/content/sms", `{"body": "Hi"}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		w := do(t, h, "PUT", tt.path, tt.body)
		body := decodeBody[errorResponse](t, w)
		if w.Code != tt.status || body.Error == "" {
			t.Errorf("PUT %s %s: %d %s", tt.path, tt.body, w.Code, w.Body)
		}
		if tt.status == http.StatusUnprocessableEntity && len(body.Problems) == 0 {
			t.Errorf("PUT %s %s: no problems listed", tt.path, tt.body)
		}
	}
}

func TestEditAfterApprovalConflicts(t *testing.T) {
	h, store := newTestAPI(&llm.Mock{})
	create(t, h, `["sms"]`)
	do(t, h, "POST", "/campaigns/1/content", "")
	store.SetStatus(1, campaign.StatusApproved)

	if w := do(t, h, "PUT", "/campaigns/1/content/sms", `{"body": "Hi"}`); w.Code != http.StatusConflict {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
