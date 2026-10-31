package campaign_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

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
	c, err = svc.GenerateContent(ctx, c.ID, func(_ string, _ int, s string) { streamed.WriteString(s) })
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
	store.Lens = campaign.LanguageLengths{"tr": {content.VarFirstName: 13}}
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

	c, err := svc.EditContent(ctx, c.ID, "tr", "email", campaign.Edit{Subject: "{{ first_name }}, son gün!", Body: "Merhaba {{first_name}}"})
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
		_, err := svc.EditContent(ctx, c.ID, "tr", tt.channel, tt.edit)
		var verr content.ValidationError
		if !errors.As(err, &verr) || verr[0].Path != tt.path {
			t.Errorf("edit %+v: err = %v, want a validation error at %s", tt.edit, err, tt.path)
		}
	}
	if _, err := svc.EditContent(ctx, c.ID, "tr", "email", campaign.Edit{Subject: "s", Body: "b"}); !errors.Is(err, campaign.ErrInvalid) {
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
	if _, err := svc.EditContent(ctx, c.ID, "tr", "sms", campaign.Edit{Body: "Hi"}); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("EditContent: err = %v", err)
	}
}

func TestApprove(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms", "email"})

	c, err := svc.Approve(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != campaign.StatusSending || c.ApprovedAt == nil {
		t.Fatalf("status %s, approved_at %v", c.Status, c.ApprovedAt)
	}
	// One message per consented user and channel: 120 SMS + 160 email.
	if got := store.Queued[c.ID]; got != 280 {
		t.Fatalf("queued %d messages, want 280", got)
	}

	// The content is locked, and approving twice is refused.
	if _, err := svc.Approve(ctx, c.ID, nil); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("second Approve: err = %v", err)
	}
	if _, err := svc.EditContent(ctx, c.ID, "tr", "sms", campaign.Edit{Body: "Hi"}); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("EditContent after approval: err = %v", err)
	}
}

// A scheduled approval locks the content and queues the same messages as
// sending now, but the campaign waits in "scheduled".
func TestApproveScheduled(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms", "email"})

	sendAt := time.Now().Add(2 * time.Hour)
	c, err := svc.Approve(ctx, c.ID, &sendAt)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != campaign.StatusScheduled || c.ScheduledAt == nil || !c.ScheduledAt.Equal(sendAt) || c.ApprovedAt == nil {
		t.Fatalf("status %s, scheduled_at %v, approved_at %v", c.Status, c.ScheduledAt, c.ApprovedAt)
	}
	if got := store.Queued[c.ID]; got != 280 {
		t.Fatalf("queued %d messages, want 280", got)
	}
	if p, err := svc.Progress(ctx, c.ID); err != nil || p.Done {
		t.Fatalf("progress done %v, err %v", p.Done, err)
	}
	if _, err := svc.EditContent(ctx, c.ID, "tr", "sms", campaign.Edit{Body: "Hi"}); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("EditContent while scheduled: err = %v", err)
	}
	if _, err := svc.Approve(ctx, c.ID, nil); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("Approve while scheduled: err = %v", err)
	}
}

func TestApproveSendAtMustBeInRange(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms"})

	for name, sendAt := range map[string]time.Time{
		"past":      time.Now().Add(-time.Minute),
		"too far":   time.Now().Add(campaign.MaxScheduleAhead + time.Hour),
		"zero time": {},
	} {
		if _, err := svc.Approve(ctx, c.ID, &sendAt); !errors.Is(err, campaign.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if c, _ := svc.Get(ctx, c.ID); c.Status != campaign.StatusContentReady || store.Queued[c.ID] != 0 {
		t.Fatalf("status %s, queued %d", c.Status, store.Queued[c.ID])
	}
	ok := time.Now().Add(campaign.MaxScheduleAhead - time.Hour)
	if _, err := svc.Approve(ctx, c.ID, &ok); err != nil {
		t.Fatalf("within range: %v", err)
	}
}

func TestApproveNeedsContent(t *testing.T) {
	svc, _ := newTestService(&llm.Mock{})
	ctx := context.Background()
	c, _ := svc.Create(ctx, exampleBrief, []string{"sms"})
	if _, err := svc.Approve(ctx, c.ID, nil); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("draft: err = %v", err)
	}
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, c.ID, nil); !errors.Is(err, campaign.ErrConflict) {
		t.Errorf("audience_ready: err = %v", err)
	}
	if _, err := svc.Approve(ctx, 404, nil); !errors.Is(err, campaign.ErrNotFound) {
		t.Errorf("unknown campaign: err = %v", err)
	}
}

// Content that fit when it was edited can stop fitting if longer names joined
// the audience since; approval measures again and refuses.
func TestApproveRevalidatesAgainstCurrentAudience(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms"})
	// Exactly 160 while names count as the placeholder's 14 characters.
	body := strings.Repeat("a", 160-14) + "{{first_name}}"
	if _, err := svc.EditContent(ctx, c.ID, "tr", "sms", campaign.Edit{Body: body}); err != nil {
		t.Fatal(err)
	}

	store.Lens = campaign.LanguageLengths{"tr": {content.VarFirstName: 15, content.VarCity: 10}}
	_, err := svc.Approve(ctx, c.ID, nil)
	var verr content.ValidationError
	if !errors.As(err, &verr) || verr[0].Path != "tr.sms.body" {
		t.Fatalf("err = %v, want a validation error at tr.sms.body", err)
	}
	if c, _ = svc.Get(ctx, c.ID); c.Status != campaign.StatusContentReady || store.Queued[c.ID] != 0 {
		t.Fatalf("status %s, queued %d after a failed approval", c.Status, store.Queued[c.ID])
	}

	store.Lens = campaign.LanguageLengths{"tr": {content.VarFirstName: 13, content.VarCity: 10}}
	if _, err := svc.Approve(ctx, c.ID, nil); err != nil {
		t.Fatalf("approve after the audience shrank back: %v", err)
	}
}

// The store approves only the content versions the service validated, so an
// edit that lands between validation and approval is not sent unvalidated.
func TestApproveRefusesChangedContent(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms"})
	validated := map[string]int{"tr.sms": c.Contents[0].Version}
	if _, err := svc.EditContent(ctx, c.ID, "tr", "sms", campaign.Edit{Body: "Hi"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(ctx, c.ID, validated, time.Now(), nil); !errors.Is(err, campaign.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
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

	store.Lens = campaign.LanguageLengths{"tr": {content.VarFirstName: 15, content.VarCity: 0}}
	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 15 as measured; the empty city measurement counts as UnknownLength.
	if c.VariableLengths["tr"]["first_name"] != 15 || c.VariableLengths["tr"]["city"] != content.UnknownLength {
		t.Fatalf("variable lengths = %v", c.VariableLengths)
	}

	// An edit that the counter would show as exactly 160 is accepted.
	c, _ = svc.GenerateContent(ctx, c.ID, nil)
	body := strings.Repeat("a", 160-c.VariableLengths["tr"]["first_name"]) + "{{first_name}}"
	if _, err := svc.EditContent(ctx, c.ID, "tr", "sms", campaign.Edit{Body: body}); err != nil {
		t.Fatalf("160 by the exposed lengths was rejected: %v", err)
	}
}

// bilingual makes the in-memory audience read Turkish and English; English
// names are longer.
func bilingual(store *campaigntest.MemStore) {
	store.Lens = campaign.LanguageLengths{
		content.LanguageTR: {content.VarFirstName: 13, content.VarCity: 10},
		content.LanguageEN: {content.VarFirstName: 20, content.VarCity: 10},
	}
}

// Each language in the audience gets its own messages, written and
// validated with that language's lengths, and streamed and recorded with it.
func TestGenerateContentPerLanguage(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	bilingual(store)
	ctx := context.Background()
	c, _ := svc.Create(ctx, exampleBrief, []string{"sms", "email"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}

	streamed := map[string]string{}
	c, err := svc.GenerateContent(ctx, c.ID, func(lang string, _ int, s string) { streamed[lang] += s })
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range c.Contents {
		got = append(got, m.Language+"."+m.Channel)
	}
	if !slices.Equal(got, []string{"tr.sms", "tr.email", "en.sms", "en.email"}) || !slices.Equal(c.Languages, []string{"tr", "en"}) {
		t.Fatalf("contents %v, languages %v", got, c.Languages)
	}
	if !strings.Contains(c.Contents[0].Body, "%30 indirim") || !strings.Contains(c.Contents[2].Body, "30% off") {
		t.Fatalf("tr sms %q, en sms %q", c.Contents[0].Body, c.Contents[2].Body)
	}
	if !strings.Contains(streamed["tr"], "indirim") || !strings.Contains(streamed["en"], "off") {
		t.Fatalf("streamed %v", streamed)
	}
	if c.VariableLengths["en"]["first_name"] != 20 || c.VariableLengths["tr"]["first_name"] != 14 { // never below the placeholder
		t.Fatalf("variable lengths %v", c.VariableLengths)
	}
	var recorded []string
	for _, a := range store.Attempts(c.ID) {
		if a.Kind == llm.KindContent {
			recorded = append(recorded, a.Language)
		}
	}
	if !slices.Equal(recorded, []string{"tr", "en"}) {
		t.Fatalf("content attempts recorded for %v", recorded)
	}
}

// If one language fails, nothing is saved and the problems name the language.
func TestGenerateContentAllLanguagesOrNothing(t *testing.T) {
	tooLong := llm.ScriptedAnswer{Text: `{"sms": {"body": "` + strings.Repeat("a", 170) + `"}}`}
	svc, store := newTestService(llm.NewScripted(
		llm.ScriptedAnswer{Text: `{"match": "all", "conditions": []}`},
		llm.ScriptedAnswer{Text: `{"sms": {"body": "Merhaba {{first_name}}"}}`},
		tooLong, tooLong, tooLong,
	))
	bilingual(store)
	ctx := context.Background()
	c, _ := svc.Create(ctx, "everyone", []string{"sms"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.GenerateContent(ctx, c.ID, nil)
	var verr content.ValidationError
	if !errors.As(err, &verr) || verr[0].Path != "en.sms.body" || !strings.HasPrefix(err.Error(), "English messages: ") {
		t.Fatalf("err = %v", err)
	}
	if c, _ = svc.Get(ctx, c.ID); c.Status != campaign.StatusAudienceReady || len(c.Contents) != 0 || c.LastError == "" {
		t.Fatalf("after a failed language: status %s, %d contents, last error %q", c.Status, len(c.Contents), c.LastError)
	}
}

// Edits change one language's message and are checked with its lengths;
// approval reports problems under the language.
func TestEditAndApprovePerLanguage(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	bilingual(store)
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms"})

	// 140 + 20 fits English names exactly; 141 does not.
	fits := strings.Repeat("a", 140) + "{{first_name}}"
	if _, err := svc.EditContent(ctx, c.ID, "en", "sms", campaign.Edit{Body: "a" + fits}); err == nil {
		t.Fatal("English edit over the limit was accepted")
	}
	c, err := svc.EditContent(ctx, c.ID, "en", "sms", campaign.Edit{Body: fits})
	if err != nil {
		t.Fatal(err)
	}
	if c.Contents[1].Body != fits || !c.Contents[1].HumanEdited || c.Contents[0].HumanEdited {
		t.Fatalf("contents %+v", c.Contents)
	}

	store.Lens["en"][content.VarFirstName] = 25 // longer English names joined
	_, err = svc.Approve(ctx, c.ID, nil)
	var verr content.ValidationError
	if !errors.As(err, &verr) || len(verr) != 1 || verr[0].Path != "en.sms.body" {
		t.Fatalf("approve: err = %v", err)
	}
}

// Approval refuses when the audience gained a language without messages.
func TestApproveNeedsEveryLanguage(t *testing.T) {
	svc, store := newTestService(&llm.Mock{})
	ctx := context.Background()
	c := readyCampaign(t, svc, []string{"sms"}) // Turkish only

	bilingual(store)
	if c, _ := svc.Get(ctx, c.ID); !slices.Equal(c.Languages, []string{"tr", "en"}) {
		t.Fatalf("languages %v, want en listed as needed", c.Languages)
	}
	if _, err := svc.Approve(ctx, c.ID, nil); !errors.Is(err, campaign.ErrConflict) || !strings.Contains(err.Error(), "English") {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, c.ID, nil); err != nil {
		t.Fatalf("approve with both languages: %v", err)
	}
}

// The audience list pages through the rule's matches without filtering on
// consent, and needs a segment.
func TestRecipients(t *testing.T) {
	svc, _ := newTestService(&llm.Mock{})
	ctx := context.Background()
	c, _ := svc.Create(ctx, exampleBrief, []string{"sms", "email"})
	if _, err := svc.Recipients(ctx, c.ID, campaign.PeopleQuery{}); !errors.Is(err, campaign.ErrConflict) {
		t.Fatalf("without a segment: err = %v", err)
	}
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}

	p, err := svc.Recipients(ctx, c.ID, campaign.PeopleQuery{Limit: 2})
	if err != nil || p.Total != 3 || len(p.Recipients) != 2 || p.Limit != 2 || p.Recipients[1].SMSConsent {
		t.Fatalf("first page %+v, err %v", p, err)
	}
	if p, _ = svc.Recipients(ctx, c.ID, campaign.PeopleQuery{Limit: 2, Offset: 2}); len(p.Recipients) != 1 || p.Recipients[0].Language != "en" {
		t.Fatalf("second page %+v", p)
	}
	if p, _ = svc.Recipients(ctx, c.ID, campaign.PeopleQuery{Offset: 10}); p.Recipients == nil || len(p.Recipients) != 0 || p.Limit != campaign.DefaultPeopleLimit {
		t.Fatalf("past the end %+v", p)
	}
	for _, tt := range [][2]int{{-1, 0}, {campaign.MaxPeopleLimit + 1, 0}, {10, -1}} {
		if _, err := svc.Recipients(ctx, c.ID, campaign.PeopleQuery{Limit: tt[0], Offset: tt[1]}); !errors.Is(err, campaign.ErrInvalid) {
			t.Errorf("limit %d offset %d: err = %v", tt[0], tt[1], err)
		}
	}
	if _, err := svc.Recipients(ctx, 404, campaign.PeopleQuery{}); !errors.Is(err, campaign.ErrNotFound) {
		t.Errorf("unknown campaign: err = %v", err)
	}
	// Sorting by a whitelisted column, either way; anything else is refused.
	names := func(q campaign.PeopleQuery) []string {
		p, err := svc.Recipients(ctx, c.ID, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range p.Recipients {
			out = append(out, r.FirstName)
		}
		return out
	}
	if got := names(campaign.PeopleQuery{Sort: "first_name"}); !slices.Equal(got, []string{"Ayşe", "Emily", "Mehmet"}) {
		t.Errorf("by name: %v", got)
	}
	if got := names(campaign.PeopleQuery{Sort: "city", Order: "desc"}); !slices.Equal(got, []string{"Emily", "Ayşe", "Mehmet"}) {
		t.Errorf("by city, descending: %v", got) // İzmir, İstanbul, Ankara (byte order in the memory store)
	}
	if p, _ := svc.Recipients(ctx, c.ID, campaign.PeopleQuery{}); p.Sort != "id" || p.Order != "asc" {
		t.Errorf("defaults: sort %q order %q", p.Sort, p.Order)
	}
	for _, q := range []campaign.PeopleQuery{{Sort: "phone"}, {Sort: "u.id; DROP TABLE users"}, {Order: "up"}} {
		if _, err := svc.Recipients(ctx, c.ID, q); !errors.Is(err, campaign.ErrInvalid) {
			t.Errorf("%+v: err = %v", q, err)
		}
	}
}
