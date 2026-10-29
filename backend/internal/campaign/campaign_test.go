package campaign

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

// memStore is an in-memory Store with fixed audience numbers. It applies the
// same status guards as PGStore.
type memStore struct {
	mu        sync.Mutex
	campaigns map[int64]*Campaign
	nextID    int64
	audience  segment.Audience
	lens      content.MaxLengths
	attempts  map[int64]*llm.MemoryRecorder
}

func newMemStore() *memStore {
	return &memStore{
		campaigns: map[int64]*Campaign{},
		audience:  segment.Audience{Matched: 230, SMS: 120, Email: 160},
		lens:      content.MaxLengths{content.VarFirstName: 13, content.VarCity: 10},
		attempts:  map[int64]*llm.MemoryRecorder{},
	}
}

var testCatalog = segment.Catalog{"city": {"Ankara", "İstanbul", "İzmir"}, "language": {"en", "tr"}}

func (s *memStore) Create(_ context.Context, brief string, channels []string) (Campaign, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	c := &Campaign{ID: s.nextID, Brief: brief, Status: StatusDraft, Channels: channels, Contents: []Content{}}
	s.campaigns[c.ID] = c
	return *c, nil
}

func (s *memStore) Get(_ context.Context, id int64) (Campaign, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.campaigns[id]
	if !ok {
		return Campaign{}, ErrNotFound
	}
	out := *c
	out.Contents = slices.Clone(c.Contents)
	return out, nil
}

func (s *memStore) guard(id int64, allowed ...string) (*Campaign, error) {
	c, ok := s.campaigns[id]
	if !ok {
		return nil, ErrNotFound
	}
	if !slices.Contains(allowed, c.Status) {
		return nil, fmt.Errorf("%w (status %s)", ErrConflict, c.Status)
	}
	return c, nil
}

func (s *memStore) SaveSegment(_ context.Context, id int64, r segment.Rule, a segment.Audience) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.guard(id, editable...)
	if err != nil {
		return err
	}
	c.Rule, c.Audience, c.Status, c.LastError, c.Contents = &r, &a, StatusAudienceReady, "", []Content{}
	return nil
}

func (s *memStore) SaveContent(_ context.Context, id int64, cc content.Content) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.guard(id, StatusAudienceReady, StatusContentReady)
	if err != nil {
		return err
	}
	version := 1
	if len(c.Contents) > 0 {
		version = c.Contents[0].Version + 1
	}
	c.Contents = nil
	if cc.SMS != nil {
		c.Contents = append(c.Contents, Content{Channel: "sms", Body: cc.SMS.Body, Version: version})
	}
	if cc.Email != nil {
		c.Contents = append(c.Contents, Content{Channel: "email", Subject: cc.Email.Subject, Body: cc.Email.Body, Version: version})
	}
	c.Status, c.LastError = StatusContentReady, ""
	return nil
}

func (s *memStore) EditContent(_ context.Context, id int64, cc content.Content, channel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.guard(id, StatusContentReady)
	if err != nil {
		return err
	}
	for i := range c.Contents {
		m := &c.Contents[i]
		if m.Channel != channel {
			continue
		}
		if channel == "sms" {
			m.Body = cc.SMS.Body
		} else {
			m.Subject, m.Body = cc.Email.Subject, cc.Email.Body
		}
		m.HumanEdited = true
		m.Version++
	}
	return nil
}

func (s *memStore) SetError(_ context.Context, id int64, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.campaigns[id].LastError = msg
	return nil
}

func (s *memStore) Catalog(context.Context) (segment.Catalog, error) { return testCatalog, nil }

func (s *memStore) Count(context.Context, segment.Rule, time.Time) (segment.Audience, error) {
	return s.audience, nil
}

func (s *memStore) MaxLengths(context.Context, segment.Rule, []string, time.Time) (content.MaxLengths, error) {
	return s.lens, nil
}

func (s *memStore) Recorder(id int64) llm.Recorder {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempts[id] == nil {
		s.attempts[id] = &llm.MemoryRecorder{}
	}
	return s.attempts[id]
}

const exampleBrief = "Son 30 günde sepete ürün ekleyip satın almayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder"

func newTestService(client llm.Client) (*Service, *memStore) {
	store := newMemStore()
	return NewService(store, client), store
}

func TestCreateValidatesInput(t *testing.T) {
	svc, _ := newTestService(&llm.Mock{})
	ctx := context.Background()

	c, err := svc.Create(ctx, "  "+exampleBrief+"  ", []string{"email", "sms", "email"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Brief != exampleBrief || !slices.Equal(c.Channels, []string{"sms", "email"}) || c.Status != StatusDraft {
		t.Fatalf("campaign = %+v, want trimmed brief and channels in canonical order", c)
	}

	for _, tt := range []struct {
		brief    string
		channels []string
	}{
		{"   ", []string{"sms"}},
		{strings.Repeat("ş", MaxBriefLength+1), []string{"sms"}},
		{"brief", nil},
		{"brief", []string{"sms", "push"}},
	} {
		if _, err := svc.Create(ctx, tt.brief, tt.channels); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%.20q, %v) err = %v, want ErrInvalid", tt.brief, tt.channels, err)
		}
	}
}

func TestFlowWithMockLLM(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c, _ := svc.Create(ctx, exampleBrief, []string{"sms", "email"})

	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusAudienceReady || c.Rule == nil || len(c.Rule.Conditions) != 3 || c.Audience.Matched != 230 {
		t.Fatalf("after segment: %+v", c)
	}

	var streamed strings.Builder
	c, err = svc.GenerateContent(ctx, c.ID, func(_ int, s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusContentReady || len(c.Contents) != 2 || c.Contents[0].Channel != "sms" || c.Contents[1].Channel != "email" {
		t.Fatalf("after content: %+v", c)
	}
	if !strings.Contains(c.Contents[0].Body, "%30 indirim") || streamed.Len() == 0 {
		t.Fatalf("sms = %q, streamed %d bytes", c.Contents[0].Body, streamed.Len())
	}

	attempts := store.attempts[c.ID].Attempts
	if len(attempts) != 2 || attempts[0].Kind != llm.KindSegment || attempts[1].Kind != llm.KindContent {
		t.Fatalf("recorded attempts = %+v", attempts)
	}
}

func TestGenerateSegmentFailureIsRecorded(t *testing.T) {
	bad := llm.ScriptedAnswer{Text: `{"match": "all", "conditions": [{"field": "income", "op": "eq", "value": "high"}]}`}
	svc, store := newTestService(llm.NewScripted(bad, bad, bad))
	ctx := context.Background()
	c, _ := svc.Create(ctx, "rich users", []string{"sms"})

	_, err := svc.GenerateSegment(ctx, c.ID)
	var gerr *llm.GenerationError
	if !errors.As(err, &gerr) {
		t.Fatalf("err = %v, want GenerationError", err)
	}
	c, _ = svc.Get(ctx, c.ID)
	if c.Status != StatusDraft || !strings.Contains(c.LastError, `unknown field "income"`) {
		t.Fatalf("campaign = %+v, want draft with the error recorded", c)
	}
	if n := len(store.attempts[c.ID].Attempts); n != 1+llm.MaxRepairs {
		t.Fatalf("recorded %d attempts, want %d", n, 1+llm.MaxRepairs)
	}

	// A later successful generation clears the error.
	svc.llm = &llm.Mock{}
	if c, err = svc.GenerateSegment(ctx, c.ID); err != nil || c.LastError != "" {
		t.Fatalf("retry: campaign = %+v, err = %v", c, err)
	}
}

func TestRegeneratingSegmentDiscardsContent(t *testing.T) {
	svc, _ := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms"})

	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusAudienceReady || len(c.Contents) != 0 {
		t.Fatalf("campaign = %+v, want audience_ready without content", c)
	}
}

func TestGenerateContentNeedsAudience(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c, _ := svc.Create(ctx, exampleBrief, []string{"sms"})

	if _, err := svc.GenerateContent(ctx, c.ID, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("draft: err = %v, want ErrConflict", err)
	}

	// Users matched, but none consented to SMS.
	store.audience = segment.Audience{Matched: 50, SMS: 0, Email: 40}
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.GenerateContent(ctx, c.ID, nil)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "consented to sms") {
		t.Fatalf("no reachable users: err = %v", err)
	}
}

func TestGenerateContentUsesAudienceLengths(t *testing.T) {
	// 150 characters plus {{first_name}} fits only if names are at most 10.
	body := `{"sms": {"body": "` + strings.Repeat("a", 150) + `{{first_name}}"}}`
	client := llm.NewScripted(
		llm.ScriptedAnswer{Text: `{"match": "all", "conditions": []}`},
		llm.ScriptedAnswer{Text: body},
		llm.ScriptedAnswer{Text: `{"sms": {"body": "Hi {{first_name}}"}}`},
	)
	svc, store := newTestService(client)
	store.lens = content.MaxLengths{content.VarFirstName: 13}
	ctx := context.Background()
	c, _ := svc.Create(ctx, "everyone", []string{"sms"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	c, err := svc.GenerateContent(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Contents[0].Body != "Hi {{first_name}}" {
		t.Fatalf("body = %q, want the repaired one", c.Contents[0].Body)
	}
	if !strings.Contains(client.Requests[1].System, "{{first_name}}: counts as 14 characters") {
		t.Fatalf("content prompt does not state the measured lengths")
	}
}

func readyCampaign(t *testing.T, svc *Service, channels []string) Campaign {
	t.Helper()
	ctx := context.Background()
	c, err := svc.Create(ctx, exampleBrief, channels)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	c, err = svc.GenerateContent(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEditContent(t *testing.T) {
	svc, _ := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms", "email"})

	c, err := svc.EditContent(ctx, c.ID, "email", Edit{Subject: "{{ first_name }}, son gün!", Body: "Merhaba {{first_name}}"})
	if err != nil {
		t.Fatal(err)
	}
	email := c.Contents[1]
	if email.Subject != "{{first_name}}, son gün!" || !email.HumanEdited || email.Version != 2 {
		t.Fatalf("email = %+v, want canonical, edited, version 2", email)
	}
	if c.Contents[0].HumanEdited {
		t.Fatal("editing email marked the SMS as edited")
	}
}

func TestEditContentRevalidates(t *testing.T) {
	svc, _ := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms"})
	before := c.Contents[0]

	tests := []struct {
		channel string
		edit    Edit
		path    string
	}{
		{"sms", Edit{Body: strings.Repeat("a", 150) + "{{first_name}}"}, "sms.body"}, // 150 + 14 > 160
		{"sms", Edit{Body: "Hi {{last_name}}"}, "sms.body"},
		{"sms", Edit{Body: ""}, "sms.body"},
		{"sms", Edit{Subject: "x", Body: "Hi"}, "sms.subject"},
	}
	for _, tt := range tests {
		_, err := svc.EditContent(ctx, c.ID, tt.channel, tt.edit)
		var verr content.ValidationError
		if !errors.As(err, &verr) || verr[0].Path != tt.path {
			t.Errorf("edit %+v: err = %v, want a validation error at %s", tt.edit, err, tt.path)
		}
	}
	if _, err := svc.EditContent(ctx, c.ID, "email", Edit{Subject: "s", Body: "b"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("editing a channel the campaign does not use: err = %v", err)
	}

	c, _ = svc.Get(ctx, c.ID)
	if c.Contents[0] != before {
		t.Fatalf("rejected edits changed the content: %+v", c.Contents[0])
	}
}

func TestNothingChangesAfterApproval(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms"})
	store.campaigns[c.ID].Status = StatusApproved

	if _, err := svc.GenerateSegment(ctx, c.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("GenerateSegment: err = %v", err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); !errors.Is(err, ErrConflict) {
		t.Errorf("GenerateContent: err = %v", err)
	}
	if _, err := svc.EditContent(ctx, c.ID, "sms", Edit{Body: "Hi"}); !errors.Is(err, ErrConflict) {
		t.Errorf("EditContent: err = %v", err)
	}
}

func TestNotFound(t *testing.T) {
	svc, _ := newTestService(&llm.Mock{})
	if _, err := svc.GenerateSegment(context.Background(), 404); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
