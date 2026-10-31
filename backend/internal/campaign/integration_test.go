//go:build integration

package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

const exampleBrief = "Son 30 günde sepete ürün ekleyip satın almayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder"

// These tests run the campaign flow with the mock LLM against the seeded
// database (make test-integration). Created campaigns are deleted afterwards.

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func createForTest(t *testing.T, svc *Service, db *pgxpool.Pool, channels []string) Campaign {
	t.Helper()
	c, err := svc.Create(context.Background(), exampleBrief, channels)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(context.Background(), "DELETE FROM campaigns WHERE id = $1", c.ID) })
	return c
}

func TestIntegrationFlow(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})

	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusAudienceReady || c.Audience == nil || c.Audience.Matched == 0 ||
		c.Audience.SMS > c.Audience.Matched || c.Audience.Email > c.Audience.Matched {
		t.Fatalf("after segment: status %s, audience %+v", c.Status, c.Audience)
	}
	if tr, en := c.Audience.Languages["tr"], c.Audience.Languages["en"]; tr.Matched == 0 || en.Matched == 0 ||
		tr.Matched+en.Matched != c.Audience.Matched || tr.SMS+en.SMS != c.Audience.SMS {
		t.Fatalf("audience per language %+v does not add up to %+v", c.Audience.Languages, c.Audience)
	}
	if again, err := svc.Get(ctx, c.ID); err != nil || !reflect.DeepEqual(again.Audience, c.Audience) {
		t.Fatalf("stored audience %+v, err %v", again.Audience, err)
	}

	c, err = svc.GenerateContent(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The seeded audience reads both languages: tr and en messages per channel.
	if c.Status != StatusContentReady || len(c.Contents) != 4 || c.Contents[1].Subject == "" || c.Contents[0].Version != 1 {
		t.Fatalf("after content: %+v", c)
	}
	if !slices.Equal(c.Languages, []string{"tr", "en"}) || c.Contents[0].Language != "tr" || c.Contents[2].Language != "en" ||
		!strings.Contains(c.Contents[0].Body, "%30 indirim") || !strings.Contains(c.Contents[2].Body, "30% off") {
		t.Fatalf("languages %v, contents %+v", c.Languages, c.Contents)
	}
	var generated []string
	if err := db.QueryRow(ctx, `SELECT array_agg(language ORDER BY id) FROM llm_generations
		WHERE campaign_id = $1 AND kind = 'content'`, c.ID).Scan(&generated); err != nil || !slices.Equal(generated, []string{"tr", "en"}) {
		t.Fatalf("content attempts recorded for %v, err %v", generated, err)
	}

	c, err = svc.EditContent(ctx, c.ID, "tr", "sms", Edit{Body: "{{first_name}}, %30 indirim bugün bitiyor!"})
	if err != nil {
		t.Fatal(err)
	}
	if sms := c.Contents[0]; !sms.HumanEdited || sms.Version != 2 || c.Contents[1].HumanEdited {
		t.Fatalf("after edit: %+v", c.Contents)
	}

	// Regenerating content resets the edit flag and bumps the version.
	if c, err = svc.GenerateContent(ctx, c.ID, nil); err != nil || c.Contents[0].HumanEdited || c.Contents[0].Version != 3 {
		t.Fatalf("regenerated: %+v, err = %v", c.Contents, err)
	}

	var kinds []string
	var parsed, validationErrors []byte
	rows, err := db.Query(ctx, `SELECT kind, parsed, validation_errors FROM llm_generations
		WHERE campaign_id = $1 ORDER BY id`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind, &parsed, &validationErrors); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
	}
	// Content is generated twice (initial and regenerated), once per language each time.
	if strings.Join(kinds, ",") != "segment,content,content,content,content" || !json.Valid(parsed) || validationErrors != nil {
		t.Fatalf("llm_generations: kinds %v, parsed %s, validation_errors %s", kinds, parsed, validationErrors)
	}
}

func TestIntegrationRecordsInvalidAttempts(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	bad := llm.ScriptedAnswer{Text: `{"match": "all", "conditions": [{"field": "income", "op": "eq", "value": "x"}]}`}
	svc := NewService(NewPGStore(db), llm.NewScripted(bad, bad, bad))
	c := createForTest(t, svc, db, []string{"sms"})

	if _, err := svc.GenerateSegment(ctx, c.ID); err == nil {
		t.Fatal("want a generation error")
	}
	var n int
	var path string
	err := db.QueryRow(ctx, `SELECT count(*), min(validation_errors->0->>'path') FROM llm_generations
		WHERE campaign_id = $1 AND NOT valid AND parsed IS NULL`, c.ID).Scan(&n, &path)
	if err != nil || n != 1+llm.MaxRepairs || path != "conditions[0].field" {
		t.Fatalf("invalid attempts = %d, first path %q, err %v", n, path, err)
	}
	if c, _ = svc.Get(ctx, c.ID); c.Status != StatusDraft || c.LastError == "" {
		t.Fatalf("campaign = %+v", c)
	}
}

func TestIntegrationMaxLengths(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	store := NewPGStore(db)
	svc := NewService(store, &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms"})
	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}

	lens, err := store.MaxLengths(ctx, *c.Rule, c.Channels, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The rule targets İstanbul, so the longest city is exactly "İstanbul"
	// (8 characters, not 9 bytes); names are at most "Muhammed Emin" (13).
	// Both languages are in the seeded audience and measured separately.
	if len(lens) != 2 {
		t.Fatalf("languages measured = %v, want tr and en", lens)
	}
	for lang, l := range lens {
		if l[content.VarCity] != 8 || l[content.VarFirstName] < 3 || l[content.VarFirstName] > 13 {
			t.Fatalf("%s lengths = %v", lang, l)
		}
	}
}

func TestIntegrationWritesStopAfterApproval(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	store := NewPGStore(db)
	svc := NewService(store, &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	c, err := svc.GenerateContent(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE campaigns SET status = 'approved', approved_at = now() WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}

	// The store itself refuses, even if a caller skipped the service checks.
	sms := content.Content{SMS: &content.SMS{Body: "changed"}}
	for name, err := range map[string]error{
		"SaveSegment": store.SaveSegment(ctx, c.ID, *c.Rule, *c.Audience),
		"SaveContent": store.SaveContent(ctx, c.ID, map[string]content.Content{"tr": sms}),
		"EditContent": store.EditContent(ctx, c.ID, "tr", sms, "sms"),
	} {
		if !errors.Is(err, ErrConflict) {
			t.Errorf("%s: err = %v, want ErrConflict", name, err)
		}
	}
	if got, _ := svc.Get(ctx, c.ID); got.Contents[0].Body != c.Contents[0].Body {
		t.Fatal("approved content changed")
	}
}

func TestIntegrationApproveEnqueues(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}

	c, err := svc.Approve(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusSending || c.ApprovedAt == nil {
		t.Fatalf("status %s, approved_at %v", c.Status, c.ApprovedAt)
	}

	// One queued message per user and channel, matching the audience counts,
	// and only to users who consented to that channel.
	// Each message keeps its recipient's language.
	var sms, email, queued, unconsented, wrongLanguage, english int
	err = db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE m.channel = 'sms'),
		       count(*) FILTER (WHERE m.channel = 'email'),
		       count(*) FILTER (WHERE m.status = 'queued' AND m.attempts = 0),
		       count(*) FILTER (WHERE (m.channel = 'sms' AND NOT u.sms_opt_in) OR (m.channel = 'email' AND NOT u.email_opt_in)),
		       count(*) FILTER (WHERE m.language <> u.language),
		       count(*) FILTER (WHERE m.language = 'en')
		FROM messages m JOIN users u ON u.id = m.user_id WHERE m.campaign_id = $1`, c.ID).
		Scan(&sms, &email, &queued, &unconsented, &wrongLanguage, &english)
	if err != nil {
		t.Fatal(err)
	}
	if sms != c.Audience.SMS || email != c.Audience.Email || queued != sms+email || unconsented != 0 || sms == 0 || email == 0 {
		t.Fatalf("sms %d (want %d), email %d (want %d), queued %d, unconsented %d",
			sms, c.Audience.SMS, email, c.Audience.Email, queued, unconsented)
	}
	en := c.Audience.Languages["en"]
	if wrongLanguage != 0 || english != en.SMS+en.Email {
		t.Fatalf("messages in a language other than the recipient's: %d; English %d, want %d", wrongLanguage, english, en.SMS+en.Email)
	}

	// Enqueueing again adds nothing.
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		n, err := enqueue(ctx, tx, c.ID, *c.Rule, "sms", time.Now(), nil)
		if err == nil && n != 0 {
			err = fmt.Errorf("re-enqueue inserted %d messages", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, c.ID, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("second approve: err = %v", err)
	}
}

// An SMS-only campaign queues no email, even to users who consented to
// email, and reaches every matched SMS-consented user exactly once, including
// those who did not consent to email.
func TestIntegrationApproveOnlyCampaignChannels(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	c, err := svc.Approve(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	var sms, email, distinct, smsOnlyUsers int
	err = db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE m.channel = 'sms'),
		       count(*) FILTER (WHERE m.channel = 'email'),
		       count(DISTINCT m.user_id),
		       count(*) FILTER (WHERE NOT u.email_opt_in)
		FROM messages m JOIN users u ON u.id = m.user_id WHERE m.campaign_id = $1`, c.ID).
		Scan(&sms, &email, &distinct, &smsOnlyUsers)
	if err != nil {
		t.Fatal(err)
	}
	if email != 0 || sms != c.Audience.SMS || distinct != sms || smsOnlyUsers == 0 {
		t.Fatalf("sms %d (want %d), email %d (want 0), distinct users %d, sms-only users %d",
			sms, c.Audience.SMS, email, distinct, smsOnlyUsers)
	}
}

func TestIntegrationApproveUnreachableAudience(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	// The audience is recomputed at approval; simulate it having emptied.
	_, err := db.Exec(ctx, `UPDATE campaigns SET segment_rule = '{"match": "all", "conditions": [{"field": "city", "op": "eq", "value": "Nowhere"}]}' WHERE id = $1`, c.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Approve(ctx, c.ID, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM messages WHERE campaign_id = $1", c.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("messages = %d, err %v", n, err)
	}
	if c, _ = svc.Get(ctx, c.ID); c.Status != StatusContentReady || c.ApprovedAt != nil {
		t.Fatalf("status %s, approved_at %v", c.Status, c.ApprovedAt)
	}
}

func TestIntegrationProgressAndList(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	if p, err := svc.Progress(ctx, c.ID); err != nil || p.Totals.Total != 0 || len(p.Channels) != 2 || p.Done {
		t.Fatalf("before approval: %+v, err %v", p, err)
	}
	c, err := svc.Approve(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate some delivery: two sent, three failed with two reasons.
	_, err = db.Exec(ctx, `
		WITH picked AS (SELECT id, row_number() OVER (ORDER BY id) AS n FROM messages WHERE campaign_id = $1 AND channel = 'sms')
		UPDATE messages m SET
		    status = CASE WHEN p.n <= 2 THEN 'sent' ELSE 'failed' END,
		    sent_at = CASE WHEN p.n <= 2 THEN now() END,
		    last_error = CASE WHEN p.n IN (3, 4) THEN 'provider returned 400: bad phone' WHEN p.n = 5 THEN 'lease expired' END
		FROM picked p WHERE m.id = p.id AND p.n <= 5`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.Progress(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	sms, email := p.Channels["sms"], p.Channels["email"]
	if sms.Total != c.Audience.SMS || sms.Sent != 2 || sms.Failed != 3 || sms.Queued != c.Audience.SMS-5 ||
		email.Total != c.Audience.Email || email.Queued != email.Total || p.Totals.Total != sms.Total+email.Total {
		t.Fatalf("progress = %+v (audience %+v)", p, c.Audience)
	}
	if len(p.Failures) != 2 || p.Failures[0] != (FailureReason{Error: "provider returned 400: bad phone", Count: 2}) {
		t.Fatalf("failures = %+v", p.Failures)
	}

	list, err := svc.List(ctx, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(list, func(s Summary) bool { return s.ID == c.ID })
	if i < 0 || list[i].Messages != p.Totals || list[i].Status != StatusSending || list[i].ApprovedAt == nil {
		t.Fatalf("list entry = %+v, want messages %+v", list, p.Totals)
	}
	for j := 1; j < len(list); j++ {
		if list[j].ID >= list[j-1].ID {
			t.Fatal("list is not newest first")
		}
	}
	if older, err := svc.List(ctx, 1, c.ID); err != nil || len(older) == 1 && older[0].ID >= c.ID {
		t.Fatalf("before %d: %+v, err %v", c.ID, older, err)
	}
	if _, err := svc.Progress(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown campaign: %v", err)
	}
}

// Two approvals racing: the campaign row lock lets exactly one through, and
// every recipient is queued once per channel.
func TestIntegrationConcurrentApprovals(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}

	const n = 5
	errs := make(chan error, n)
	start := make(chan struct{})
	for range n {
		go func() {
			<-start
			_, err := svc.Approve(ctx, c.ID, nil)
			errs <- err
		}()
	}
	close(start)
	ok := 0
	for range n {
		switch err := <-errs; {
		case err == nil:
			ok++
		case !errors.Is(err, ErrConflict):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d approvals succeeded, want 1", ok)
	}

	c, _ = svc.Get(ctx, c.ID)
	var total, distinct int
	err := db.QueryRow(ctx, `SELECT count(*), count(DISTINCT (user_id, channel)) FROM messages WHERE campaign_id = $1`, c.ID).Scan(&total, &distinct)
	if err != nil || total != distinct || total != c.Audience.SMS+c.Audience.Email {
		t.Fatalf("messages %d, distinct %d, want %d (err %v)", total, distinct, c.Audience.SMS+c.Audience.Email, err)
	}
}

// Approval needs messages for every language in today's audience. The store
// on its own never queues a recipient whose language has no content, so the
// worker cannot be left with a message it has nothing to send for.
func TestIntegrationApproveNeedsEveryLanguage(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	store := NewPGStore(db)
	svc := NewService(store, &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM campaign_contents WHERE campaign_id = $1 AND language = 'en'", c.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Approve(ctx, c.ID, nil); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "English") {
		t.Fatalf("approve without English messages: err = %v", err)
	}

	c, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(ctx, c.ID, map[string]int{"tr.sms": c.Contents[0].Version}, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	var queued, english int
	if err := db.QueryRow(ctx, "SELECT count(*), count(*) FILTER (WHERE language = 'en') FROM messages WHERE campaign_id = $1",
		c.ID).Scan(&queued, &english); err != nil {
		t.Fatal(err)
	}
	if want := c.Audience.Languages["tr"].SMS; queued != want || english != 0 {
		t.Fatalf("queued %d (want the %d Turkish recipients), English %d", queued, want, english)
	}
}

// The audience list pages through exactly the users the rule matches, with
// their consent as stored, stable by id.
func TestIntegrationRecipients(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})
	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}

	var all []Person
	for offset := 0; ; offset += 40 {
		p, err := svc.Recipients(ctx, c.ID, PeopleQuery{Limit: 40, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != c.Audience.Matched {
			t.Fatalf("total %d, audience matched %d", p.Total, c.Audience.Matched)
		}
		if len(p.Recipients) == 0 {
			break
		}
		all = append(all, p.Recipients...)
	}
	sms, email := 0, 0
	for i, p := range all {
		if i > 0 && p.ID <= all[i-1].ID {
			t.Fatalf("not ordered by id at %d: %d after %d", i, p.ID, all[i-1].ID)
		}
		if p.City != "İstanbul" || p.FirstName == "" || p.Phone == "" || p.Email == "" || (p.Language != "tr" && p.Language != "en") {
			t.Fatalf("person %+v", p)
		}
		if p.SMSConsent {
			sms++
		}
		if p.EmailConsent {
			email++
		}
	}
	if len(all) != c.Audience.Matched || sms != c.Audience.SMS || email != c.Audience.Email {
		t.Fatalf("listed %d (sms %d, email %d), audience %+v", len(all), sms, email, c.Audience)
	}
}

// Sorting covers every page, with Turkish alphabetical order (Ç after C, not
// after Z) and ties broken by id, matching a hand-written reference query.
func TestIntegrationRecipientsSorted(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})
	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ sort, order, ref string }{
		{"first_name", "asc", `u.first_name COLLATE "tr-TR-x-icu" ASC, u.id`},
		{"first_name", "desc", `u.first_name COLLATE "tr-TR-x-icu" DESC, u.id`},
		{"city", "desc", `u.city COLLATE "tr-TR-x-icu" DESC, u.id`},
		{"language", "asc", `u.language ASC, u.id`},
	} {
		var got []int64
		for offset := 0; ; offset += 50 {
			p, err := svc.Recipients(ctx, c.ID, PeopleQuery{Limit: 50, Offset: offset, Sort: tt.sort, Order: tt.order})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Recipients) == 0 {
				break
			}
			for _, r := range p.Recipients {
				got = append(got, r.ID)
			}
		}
		where, args, err := segment.Where(*c.Rule, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var want []int64
		if err := db.QueryRow(ctx, "SELECT array_agg(u.id ORDER BY "+tt.ref+") FROM users u WHERE "+where, args...).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s %s: order differs from the reference (%d vs %d ids)", tt.sort, tt.order, len(got), len(want))
		}
	}

	// The collation puts Turkish letters in their place.
	var cagri, ceren, zeynep int
	err = db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM (VALUES ('Ceren'), ('Zeynep')) v(n) WHERE n COLLATE "tr-TR-x-icu" < 'Çağrı'),
		(SELECT count(*) FROM (VALUES ('Çağrı'), ('Zeynep')) v(n) WHERE n COLLATE "tr-TR-x-icu" < 'Ceren'),
		(SELECT count(*) FROM (VALUES ('Çağrı'), ('Ceren')) v(n) WHERE n COLLATE "tr-TR-x-icu" < 'Zeynep')`).Scan(&cagri, &ceren, &zeynep)
	if err != nil || cagri != 1 || ceren != 0 || zeynep != 2 {
		t.Fatalf("Turkish collation: Çağrı after %d, Ceren after %d, Zeynep after %d (err %v)", cagri, ceren, zeynep, err)
	}
}
