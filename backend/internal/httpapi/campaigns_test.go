package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign/campaigntest"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
)

const exampleBrief = "Son 30 günde sepete ürün ekleyip satın almayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder"

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil))) // keep test output readable
	os.Exit(m.Run())
}

func newTestAPI(client llm.Client) (http.Handler, *campaigntest.MemStore) {
	store := campaigntest.NewMemStore()
	srv := New(fakePinger{}, campaign.NewService(store, client), fakeMetrics{})
	srv.eventsInterval = 5 * time.Millisecond
	return srv.Handler(), store
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
		c.VariableLengths["tr"]["first_name"] == 0 || c.Limits["sms.body"] != 160 {
		t.Fatalf("campaign = %+v", c)
	}

	got := do(t, h, "GET", "/campaigns/1", "")
	if got.Code != http.StatusOK || decodeBody[campaign.Campaign](t, got).ID != c.ID {
		t.Fatalf("get: %d %s", got.Code, got.Body)
	}
}

func TestCreateCampaignAcceptsTrailingWhitespace(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	if w := do(t, h, "POST", "/campaigns", "{\"brief\": \"x\", \"channels\": [\"sms\"]}\n\t "); w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestCreateCampaignRejectsBadInput(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	for _, body := range []string{
		`{"brief": "x", "channels": ["fax"]}`,
		`{"brief": "", "channels": ["sms"]}`,
		`{"brief": "x", "channels": ["sms"], "segment_rule": {}}`, // the rule is never accepted from clients
		`not json`,
		`{"brief": "x", "channels": ["sms"]} {"brief": "y"}`, // a second object
		`{"brief": "x", "channels": ["sms"]}}`,               // a stray brace
		`{"brief": "x", "channels": ["sms"]} trailing`,
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
		if err := json.Unmarshal([]byte(e.data), &d); err != nil || e.name != "delta" || d.Attempt != i+1 || d.Language != "tr" {
			t.Fatalf("event %d = %+v, want delta of attempt %d", i, e, i+1)
		}
	}
	var body errorResponse
	if err := json.Unmarshal([]byte(events[3].data), &body); err != nil || events[3].name != "error" ||
		body.Status != http.StatusUnprocessableEntity || len(body.Problems) != 1 || body.Problems[0].Path != "tr.sms.body" {
		t.Fatalf("error event = %+v", events[3])
	}
}

func TestEditContent(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	create(t, h, `["sms", "email"]`)
	do(t, h, "POST", "/campaigns/1/content", "")

	w := do(t, h, "PUT", "/campaigns/1/content/tr/sms", `{"body": "{{ first_name }}, son gün!"}`)
	c := decodeBody[campaign.Campaign](t, w)
	if w.Code != http.StatusOK || c.Contents[0].Body != "{{first_name}}, son gün!" || !c.Contents[0].HumanEdited {
		t.Fatalf("%d %+v", w.Code, c.Contents)
	}

	tests := []struct {
		path, body string
		status     int
	}{
		{"/campaigns/1/content/tr/sms", `{"body": "` + strings.Repeat("a", 161) + `"}`, http.StatusUnprocessableEntity},
		{"/campaigns/1/content/tr/sms", `{"body": "Hi {{phone}}"}`, http.StatusUnprocessableEntity},
		{"/campaigns/1/content/tr/email", `{"subject": "", "body": "b"}`, http.StatusUnprocessableEntity},
		{"/campaigns/1/content/tr/push", `{"body": "Hi"}`, http.StatusBadRequest},
		{"/campaigns/1/content/en/sms", `{"body": "Hi"}`, http.StatusBadRequest}, // no English messages
		{"/campaigns/1/content/tr/sms", `{"body": "Hi", "human_edited": false}`, http.StatusBadRequest},
		{"/campaigns/1/content/tr/sms", `{"body": "Hi"}]`, http.StatusBadRequest},
		{"/campaigns/9/content/tr/sms", `{"body": "Hi"}`, http.StatusNotFound},
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

	if w := do(t, h, "PUT", "/campaigns/1/content/tr/sms", `{"body": "Hi"}`); w.Code != http.StatusConflict {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestApprove(t *testing.T) {
	h, store := newTestAPI(&llm.Mock{})
	create(t, h, `["sms", "email"]`)
	if w := do(t, h, "POST", "/campaigns/1/approve", ""); w.Code != http.StatusConflict {
		t.Fatalf("approve without content: %d %s", w.Code, w.Body)
	}
	do(t, h, "POST", "/campaigns/1/content", "")

	w := do(t, h, "POST", "/campaigns/1/approve", "")
	c := decodeBody[campaign.Campaign](t, w)
	if w.Code != http.StatusOK || c.Status != campaign.StatusSending || c.ApprovedAt == nil || store.Queued[1] == 0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}

	for path, status := range map[string]int{
		"/campaigns/1/approve": http.StatusConflict, // already approved
		"/campaigns/9/approve": http.StatusNotFound,
	} {
		if w := do(t, h, "POST", path, ""); w.Code != status || decodeBody[errorResponse](t, w).Error == "" {
			t.Errorf("POST %s: %d %s, want %d", path, w.Code, w.Body, status)
		}
	}
}

func TestApproveScheduled(t *testing.T) {
	h, store := newTestAPI(&llm.Mock{})
	create(t, h, `["sms"]`)
	do(t, h, "POST", "/campaigns/1/content", "")

	for body, status := range map[string]int{
		`{"send_at": "` + time.Now().Add(-time.Hour).Format(time.RFC3339) + `"}`: http.StatusBadRequest, // past
		`{"send_at": "tomorrow"}`: http.StatusBadRequest,
		`{"when": "now"}`:         http.StatusBadRequest, // unknown field
		`{"send_at": null} {}`:    http.StatusBadRequest, // trailing data
	} {
		if w := do(t, h, "POST", "/campaigns/1/approve", body); w.Code != status || decodeBody[errorResponse](t, w).Error == "" {
			t.Errorf("%s: %d %s, want %d", body, w.Code, w.Body, status)
		}
	}
	if store.Queued[1] != 0 {
		t.Fatal("a refused approval queued messages")
	}

	sendAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	w := do(t, h, "POST", "/campaigns/1/approve", `{"send_at": "`+sendAt.Format(time.RFC3339)+`"}`)
	c := decodeBody[campaign.Campaign](t, w)
	if w.Code != http.StatusOK || c.Status != campaign.StatusScheduled || c.ScheduledAt == nil || !c.ScheduledAt.Equal(sendAt) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestScheduleActions(t *testing.T) {
	h, store := newTestAPI(&llm.Mock{})
	schedule := func() {
		t.Helper()
		create(t, h, `["sms"]`)
		do(t, h, "POST", "/campaigns/1/content", "")
		sendAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		if w := do(t, h, "POST", "/campaigns/1/approve", `{"send_at": "`+sendAt+`"}`); w.Code != http.StatusOK {
			t.Fatalf("approve: %d %s", w.Code, w.Body)
		}
	}
	schedule()

	w := do(t, h, "DELETE", "/campaigns/1/schedule", "")
	if c := decodeBody[campaign.Campaign](t, w); w.Code != http.StatusOK || c.Status != campaign.StatusContentReady || c.ScheduledAt != nil {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	if store.Queued[1] != 0 {
		t.Fatalf("cancel left %d messages queued", store.Queued[1])
	}
	// Not scheduled any more.
	if w := do(t, h, "DELETE", "/campaigns/1/schedule", ""); w.Code != http.StatusConflict {
		t.Fatalf("second cancel: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/campaigns/1/send-now", ""); w.Code != http.StatusConflict {
		t.Fatalf("send-now on content_ready: %d %s", w.Code, w.Body)
	}

	sendAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	do(t, h, "POST", "/campaigns/1/approve", `{"send_at": "`+sendAt+`"}`)
	w = do(t, h, "POST", "/campaigns/1/send-now", "")
	if c := decodeBody[campaign.Campaign](t, w); w.Code != http.StatusOK || c.Status != campaign.StatusSending {
		t.Fatalf("send-now: %d %s", w.Code, w.Body)
	}
	// Already sending: the worker (or another tab) got there first.
	for _, req := range [][2]string{{"POST", "/campaigns/1/send-now"}, {"DELETE", "/campaigns/1/schedule"}} {
		if w := do(t, h, req[0], req[1], ""); w.Code != http.StatusConflict || decodeBody[errorResponse](t, w).Error == "" {
			t.Errorf("%s %s while sending: %d %s", req[0], req[1], w.Code, w.Body)
		}
	}
	if w := do(t, h, "POST", "/campaigns/42/send-now", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown campaign: %d", w.Code)
	}
}

// send_at is optional: null means now, like no body at all.
func TestApproveSendAtNull(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	create(t, h, `["sms"]`)
	do(t, h, "POST", "/campaigns/1/content", "")
	w := do(t, h, "POST", "/campaigns/1/approve", `{"send_at": null}`)
	if c := decodeBody[campaign.Campaign](t, w); w.Code != http.StatusOK || c.Status != campaign.StatusSending || c.ScheduledAt != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestApproveRevalidates(t *testing.T) {
	h, store := newTestAPI(&llm.Mock{})
	create(t, h, `["sms"]`)
	do(t, h, "POST", "/campaigns/1/content", "")
	body := strings.Repeat("a", 160-14) + "{{first_name}}"
	if w := do(t, h, "PUT", "/campaigns/1/content/tr/sms", `{"body": "`+body+`"}`); w.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	store.Lens["tr"][content.VarFirstName] = 20 // a longer name joined the audience

	w := do(t, h, "POST", "/campaigns/1/approve", "")
	if w.Code != http.StatusUnprocessableEntity || len(decodeBody[errorResponse](t, w).Problems) == 0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestRecipients(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	create(t, h, `["sms", "email"]`)

	w := do(t, h, "GET", "/campaigns/1/recipients?limit=2&offset=1", "")
	p := decodeBody[campaign.People](t, w)
	if w.Code != http.StatusOK || p.Total != 3 || len(p.Recipients) != 2 || p.Offset != 1 || p.Recipients[0].FirstName != "Mehmet" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for path, status := range map[string]int{
		"/campaigns/1/recipients?limit=abc":  http.StatusBadRequest,
		"/campaigns/1/recipients?limit=0":    http.StatusBadRequest,
		"/campaigns/1/recipients?limit=500":  http.StatusBadRequest,
		"/campaigns/1/recipients?offset=-1":  http.StatusBadRequest,
		"/campaigns/1/recipients?offset=one": http.StatusBadRequest,
		"/campaigns/9/recipients":            http.StatusNotFound,
		"/campaigns/1/recipients?sort=email": http.StatusBadRequest,
		"/campaigns/1/recipients?order=up":   http.StatusBadRequest,
	} {
		if w := do(t, h, "GET", path, ""); w.Code != status || decodeBody[errorResponse](t, w).Error == "" {
			t.Errorf("GET %s: %d %s, want %d", path, w.Code, w.Body, status)
		}
	}
	w = do(t, h, "GET", "/campaigns/1/recipients?sort=first_name&order=desc", "")
	if p := decodeBody[campaign.People](t, w); w.Code != http.StatusOK || p.Recipients[0].FirstName != "Mehmet" || p.Order != "desc" {
		t.Fatalf("sorted: %d %s", w.Code, w.Body)
	}

	// The list is read-only.
	if w := do(t, h, "POST", "/campaigns/1/recipients", `{"id": 4}`); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST recipients: %d", w.Code)
	}
}

func TestListCampaigns(t *testing.T) {
	h, store := newTestAPI(&llm.Mock{})
	for range 3 {
		create(t, h, `["sms"]`)
	}
	store.SetMessages(2, "sms", campaign.Counts{Sent: 5, Failed: 1}, campaign.StatusSending)

	w := do(t, h, "GET", "/campaigns", "")
	list := decodeBody[[]campaign.Summary](t, w)
	if w.Code != http.StatusOK || len(list) != 3 || list[0].ID != 3 || list[1].Messages != (campaign.Counts{Total: 6, Sent: 5, Failed: 1}) {
		t.Fatalf("%d %+v", w.Code, list)
	}
	w = do(t, h, "GET", "/campaigns?limit=1&before=3", "")
	if list = decodeBody[[]campaign.Summary](t, w); w.Code != http.StatusOK || len(list) != 1 || list[0].ID != 2 {
		t.Fatalf("page 2: %d %+v", w.Code, list)
	}
	if w = do(t, h, "GET", "/campaigns?before=1", ""); w.Code != http.StatusOK || w.Body.String() != "[]\n" {
		t.Fatalf("empty page: %d %q", w.Code, w.Body)
	}
	for _, q := range []string{"limit=0", "limit=201", "limit=-1", "limit=x", "before=0", "before=x"} {
		if w := do(t, h, "GET", "/campaigns?"+q, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", q, w.Code, w.Body)
		}
	}
}

func TestProgress(t *testing.T) {
	h, store := newTestAPI(&llm.Mock{})
	create(t, h, `["sms", "email"]`)

	w := do(t, h, "GET", "/campaigns/1/progress", "")
	p := decodeBody[campaign.Progress](t, w)
	if w.Code != http.StatusOK || len(p.Channels) != 2 || p.Totals.Total != 0 || p.Done || p.Failures == nil {
		t.Fatalf("before approval: %d %s", w.Code, w.Body)
	}

	store.SetMessages(1, "sms", campaign.Counts{Sent: 90, Retry: 5, Failed: 5}, campaign.StatusSending)
	store.SetMessages(1, "email", campaign.Counts{Queued: 50, Processing: 10}, campaign.StatusSending)
	store.Failures[1] = []campaign.FailureReason{{Error: "provider returned 400: to must be an E.164 phone number", Count: 5}}
	p = decodeBody[campaign.Progress](t, do(t, h, "GET", "/campaigns/1/progress", ""))
	want := campaign.Counts{Total: 160, Queued: 50, Processing: 10, Retry: 5, Sent: 90, Failed: 5}
	if p.Totals != want || p.Channels["sms"].Total != 100 || p.Status != campaign.StatusSending || len(p.Failures) != 1 {
		t.Fatalf("sending: %+v", p)
	}

	if w := do(t, h, "GET", "/campaigns/9/progress", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", w.Code)
	}
}

// The event stream sends progress now, again on every change, and ends with
// done once the campaign finishes.
func TestEventsStreamUntilDone(t *testing.T) {
	h, store := newTestAPI(&llm.Mock{})
	create(t, h, `["sms"]`)
	store.SetMessages(1, "sms", campaign.Counts{Queued: 10}, campaign.StatusSending)

	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/campaigns/1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	events := make(chan campaign.Progress)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var p campaign.Progress
				if json.Unmarshal([]byte(data), &p) == nil {
					events <- p
				}
			}
		}
	}()
	next := func() campaign.Progress {
		t.Helper()
		select {
		case p, ok := <-events:
			if !ok {
				t.Fatal("stream ended early")
			}
			return p
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
		}
		return campaign.Progress{}
	}

	if p := next(); p.Totals.Queued != 10 || p.Done {
		t.Fatalf("first event: %+v", p)
	}
	store.SetMessages(1, "sms", campaign.Counts{Queued: 4, Sent: 6}, campaign.StatusSending)
	if p := next(); p.Totals.Sent != 6 || p.Done {
		t.Fatalf("after progress: %+v", p)
	}
	store.SetMessages(1, "sms", campaign.Counts{Sent: 9, Failed: 1}, campaign.StatusCompleted)
	if p := next(); !p.Done || p.Status != campaign.StatusCompleted || p.Totals.Sent != 9 {
		t.Fatalf("final event: %+v", p)
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("event after done")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after done")
	}
}

func TestEventsUnknownCampaignIsPlain404(t *testing.T) {
	h, _ := newTestAPI(&llm.Mock{})
	w := do(t, h, "GET", "/campaigns/9/events", "")
	if w.Code != http.StatusNotFound || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s", w.Code, w.Header().Get("Content-Type"))
	}
}
