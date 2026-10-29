package campaign_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign/campaigntest"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

const exampleBrief = "Son 30 günde sepete ürün ekleyip satın almayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder"

func newTestService(client llm.Client) (*campaign.Service, *campaigntest.MemStore) {
	store := campaigntest.NewMemStore()
	return campaign.NewService(store, client), store
}

func TestCreateValidatesInput(t *testing.T) {
	svc, _ := newTestService(&llm.Mock{})
	ctx := context.Background()

	c, err := svc.Create(ctx, "  "+exampleBrief+"  ", []string{"email", "sms", "email"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Brief != exampleBrief || !slices.Equal(c.Channels, []string{"sms", "email"}) || c.Status != campaign.StatusDraft {
		t.Fatalf("campaign = %+v, want trimmed brief and channels in canonical order", c)
	}

	for _, tt := range []struct {
		brief    string
		channels []string
	}{
		{"   ", []string{"sms"}},
		{strings.Repeat("ş", campaign.MaxBriefLength+1), []string{"sms"}},
		{"brief", nil},
		{"brief", []string{"sms", "push"}},
	} {
		if _, err := svc.Create(ctx, tt.brief, tt.channels); !errors.Is(err, campaign.ErrInvalid) {
			t.Errorf("Create(%.20q, %v) err = %v, want campaign.ErrInvalid", tt.brief, tt.channels, err)
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
	if c.Status != campaign.StatusAudienceReady || c.Rule == nil || len(c.Rule.Conditions) != 3 || c.Audience.Matched != 230 {
		t.Fatalf("after segment: %+v", c)
	}

	var streamed strings.Builder
	c, err = svc.GenerateContent(ctx, c.ID, func(_ int, s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != campaign.StatusContentReady || len(c.Contents) != 2 || c.Contents[0].Channel != "sms" || c.Contents[1].Channel != "email" {
		t.Fatalf("after content: %+v", c)
	}
	if !strings.Contains(c.Contents[0].Body, "%30 indirim") || streamed.Len() == 0 {
		t.Fatalf("sms = %q, streamed %d bytes", c.Contents[0].Body, streamed.Len())
	}

	attempts := store.Attempts(c.ID)
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
	if c.Status != campaign.StatusDraft || !strings.Contains(c.LastError, `unknown field "income"`) {
		t.Fatalf("campaign = %+v, want draft with the error recorded", c)
	}
	if n := len(store.Attempts(c.ID)); n != 1+llm.MaxRepairs {
		t.Fatalf("recorded %d attempts, want %d", n, 1+llm.MaxRepairs)
	}

	// A later successful generation clears the error.
	svc = campaign.NewService(store, &llm.Mock{})
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
	if c.Status != campaign.StatusAudienceReady || len(c.Contents) != 0 {
		t.Fatalf("campaign = %+v, want audience_ready without content", c)
	}
}

func TestGenerateContentNeedsAudience(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c, _ := svc.Create(ctx, exampleBrief, []string{"sms"})

	if _, err := svc.GenerateContent(ctx, c.ID, nil); !errors.Is(err, campaign.ErrConflict) {
		t.Fatalf("draft: err = %v, want campaign.ErrConflict", err)
	}

	// Users matched, but none consented to SMS.
	store.Audience = segment.Audience{Matched: 50, SMS: 0, Email: 40}
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.GenerateContent(ctx, c.ID, nil)
	if !errors.Is(err, campaign.ErrConflict) || !strings.Contains(err.Error(), "consented to sms") {
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
	store.Lens = content.MaxLengths{content.VarFirstName: 13}
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

func readyCampaign(t *testing.T, svc *campaign.Service, channels []string) campaign.Campaign {
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

	c, err := svc.EditContent(ctx, c.ID, "email", campaign.Edit{Subject: "{{ first_name }}, son gün!", Body: "Merhaba {{first_name}}"})
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
		edit    campaign.Edit
		path    string
	}{
		{"sms", campaign.Edit{Body: strings.Repeat("a", 150) + "{{first_name}}"}, "sms.body"}, // 150 + 14 > 160
		{"sms", campaign.Edit{Body: "Hi {{last_name}}"}, "sms.body"},
		{"sms", campaign.Edit{Body: ""}, "sms.body"},
		{"sms", campaign.Edit{Subject: "x", Body: "Hi"}, "sms.subject"},
	}
	for _, tt := range tests {
		_, err := svc.EditContent(ctx, c.ID, tt.channel, tt.edit)
		var verr content.ValidationError
		if !errors.As(err, &verr) || verr[0].Path != tt.path {
			t.Errorf("edit %+v: err = %v, want a validation error at %s", tt.edit, err, tt.path)
		}
	}
	if _, err := svc.EditContent(ctx, c.ID, "email", campaign.Edit{Subject: "s", Body: "b"}); !errors.Is(err, campaign.ErrInvalid) {
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
	store.SetStatus(c.ID, campaign.StatusApproved)

	if _, err := svc.GenerateSegment(ctx, c.ID); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("GenerateSegment: err = %v", err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("GenerateContent: err = %v", err)
	}
	if _, err := svc.EditContent(ctx, c.ID, "sms", campaign.Edit{Body: "Hi"}); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("EditContent: err = %v", err)
	}
}

func TestNotFound(t *testing.T) {
	svc, _ := newTestService(&llm.Mock{})
	if _, err := svc.GenerateSegment(context.Background(), 404); !errors.Is(err, campaign.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// The UI's live counter needs the same numbers the server validates with.
func TestGetExposesValidationLengths(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c, _ := svc.Create(ctx, exampleBrief, []string{"sms"})
	if c.VariableLengths != nil || c.Limits["sms.body"] != 160 || c.Limits["email.subject"] != 80 {
		t.Fatalf("draft: lengths %v, limits %v", c.VariableLengths, c.Limits)
	}

	store.Lens = content.MaxLengths{content.VarFirstName: 15, content.VarCity: 0}
	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 15 as measured; the empty city measurement counts as UnknownLength.
	if c.VariableLengths["first_name"] != 15 || c.VariableLengths["city"] != content.UnknownLength {
		t.Fatalf("variable lengths = %v", c.VariableLengths)
	}

	// An edit that the counter would show as exactly 160 is accepted.
	c, _ = svc.GenerateContent(ctx, c.ID, nil)
	body := strings.Repeat("a", 160-c.VariableLengths["first_name"]) + "{{first_name}}"
	if _, err := svc.EditContent(ctx, c.ID, "sms", campaign.Edit{Body: body}); err != nil {
		t.Fatalf("160 by the exposed lengths was rejected: %v", err)
	}
}
