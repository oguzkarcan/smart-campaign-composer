package llm

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

func TestMockSegmentExampleBrief(t *testing.T) {
	want := `{"match": "all", "conditions": [
		{"field": "city", "op": "eq", "value": "İstanbul"},
		{"event": "cart_add", "within_days": 30, "exists": true},
		{"event": "purchase", "within_days": 30, "exists": false}
	]}`
	wantRule, err := segment.Parse([]byte(want), testCatalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, brief := range []string{
		"Son 30 günde sepete ürün ekleyip satın almayan İstanbul'daki kullanıcılara hatırlatma gönder",
		// The exact wording of the case document.
		"Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder.",
		"Sepetine ürün ekleyip alışveriş yapmamış İstanbul kullanıcıları",
		"Users in Istanbul who added to cart in the last 30 days but did not purchase",
	} {
		t.Run(brief, func(t *testing.T) {
			got := generateWithMock(t, brief)
			if !reflect.DeepEqual(got, wantRule) {
				t.Fatalf("rule = %+v, want %+v", got, wantRule)
			}
		})
	}
}

func TestMockSegmentVariants(t *testing.T) {
	tests := []struct {
		brief string
		want  string
	}{
		{"Everyone", `{"match": "all", "conditions": []}`},
		{"Ankara ve İzmir'de son 7 günde ziyaret eden kullanıcılar",
			`{"match": "all", "conditions": [{"field": "city", "op": "in", "value": ["Ankara", "İzmir"]}, {"event": "page_view", "within_days": 7, "exists": true}]}`},
		{"Son 60 günde alışveriş yapan Ankara kullanıcıları",
			`{"match": "all", "conditions": [{"field": "city", "op": "eq", "value": "Ankara"}, {"event": "purchase", "within_days": 60, "exists": true}]}`},
		{"Alışveriş yapmış müşteriler",
			`{"match": "all", "conditions": [{"event": "purchase", "within_days": 30, "exists": true}]}`},
		{"Customers who purchased in the last 90 days",
			`{"match": "all", "conditions": [{"event": "purchase", "within_days": 90, "exists": true}]}`},
		{"İngilizce konuşan İstanbul kullanıcıları",
			`{"match": "all", "conditions": [{"field": "city", "op": "eq", "value": "İstanbul"}, {"field": "language", "op": "eq", "value": "en"}]}`},
		{"Turkish-speaking users who added to cart",
			`{"match": "all", "conditions": [{"event": "cart_add", "within_days": 30, "exists": true}, {"field": "language", "op": "eq", "value": "tr"}]}`},
		{"Both Turkish and English speakers", `{"match": "all", "conditions": []}`}, // naming both is no filter
	}
	for _, tt := range tests {
		t.Run(tt.brief, func(t *testing.T) {
			want, err := segment.Parse([]byte(tt.want), testCatalog)
			if err != nil {
				t.Fatal(err)
			}
			if got := generateWithMock(t, tt.brief); !reflect.DeepEqual(got, want) {
				t.Fatalf("rule = %+v, want %+v", got, want)
			}
		})
	}
}

func generateWithMock(t *testing.T, brief string) segment.Rule {
	t.Helper()
	req := SegmentRequest(SegmentInput{Brief: brief, Catalog: testCatalog})
	r, err := Generate(context.Background(), &Mock{}, &MemoryRecorder{}, req, parseRule, nil)
	if err != nil {
		t.Fatalf("mock output is invalid: %v", err)
	}
	return r
}

// Every mock template must pass validation on the first attempt, even when
// variable lengths are unknown (the most pessimistic case).
func TestMockContentIsValid(t *testing.T) {
	cart := segment.Rule{Match: "all", Conditions: []segment.Condition{{Event: "cart_add", WithinDays: 30, Exists: new(true)}}}
	english := segment.Rule{Match: "all", Conditions: []segment.Condition{{Field: "language", Op: "eq", Value: segment.Values{"en"}}}}
	channelSets := [][]string{{"sms"}, {"email"}, {"sms", "email"}}
	for _, brief := range []string{"Sepeti terk eden kullanıcılar", "Users who left their cart",
		"Sepeti terk edenlere %30 indirim", "Users who left their cart, 30% off"} {
		for _, rule := range []segment.Rule{{Match: "all"}, cart, english} {
			for _, channels := range channelSets {
				in := ContentInput{Brief: brief, Channels: channels, Rule: rule, Lengths: content.MaxLengths{}}
				rec := &MemoryRecorder{}
				parse := func(b []byte) (content.Content, error) { return content.Parse(b, channels, in.Lengths) }
				if _, err := Generate(context.Background(), &Mock{}, rec, ContentRequest(in), parse, nil); err != nil || len(rec.Attempts) != 1 {
					t.Fatalf("brief %q, rule %+v, channels %v: err = %v, attempts = %d", brief, rule, channels, err, len(rec.Attempts))
				}
			}
		}
	}
}

func TestMockContentLanguage(t *testing.T) {
	sms := func(brief string, rule segment.Rule) string {
		c := mockContent(ContentInput{Brief: brief, Channels: []string{"sms"}, Rule: rule})
		return c.SMS.Body
	}
	if got := sms("Sepete ekleyip almayanlar", segment.Rule{}); !strings.Contains(got, "seni bekliyor") {
		t.Errorf("Turkish brief got %q", got)
	}
	if got := sms("New offers", segment.Rule{}); !strings.Contains(got, "waiting") {
		t.Errorf("English brief got %q", got)
	}
	english := segment.Rule{Conditions: []segment.Condition{{Field: "language", Op: "eq", Value: segment.Values{"en"}}}}
	if got := sms("İngilizce konuşan müşteriler", english); !strings.Contains(got, "waiting") {
		t.Errorf("language=en audience got %q", got)
	}
}

func TestMockStreamsTheWholeAnswer(t *testing.T) {
	var streamed strings.Builder
	req := ContentRequest(ContentInput{Brief: "Sepet hatırlatması", Channels: []string{"sms", "email"}})
	resp, err := (&Mock{}).Complete(context.Background(), req, func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != resp.Text || !json.Valid([]byte(resp.Text)) {
		t.Fatalf("streamed %q, answer %q", streamed.String(), resp.Text)
	}
}

func TestMockIsDeterministic(t *testing.T) {
	req := SegmentRequest(SegmentInput{Brief: "İzmir sepet", Catalog: testCatalog})
	a, _ := (&Mock{}).Complete(context.Background(), req, nil)
	b, _ := (&Mock{}).Complete(context.Background(), req, nil)
	if a.Text != b.Text {
		t.Fatal("same input gave different output")
	}
}

func TestMockSegmentLanguageNeedsCatalog(t *testing.T) {
	r := mockSegment(SegmentInput{Brief: "English speakers", Catalog: segment.Catalog{"city": {"Ankara"}}})
	if len(r.Conditions) != 0 {
		t.Fatalf("rule = %+v, want no language condition without a language catalog", r)
	}
}

func TestMockDiscount(t *testing.T) {
	tests := map[string]int{
		"Sepetini terk edenlere %30 indirim":     30,
		"İndirim: % 15":                          15,
		"Remind them with 20% off":               20,
		"yüzde 25 indirim":                       25,
		"Son 30 günde sepete ekleyenler":         0, // a day count is not a discount
		"%100 indirim":                           0, // out of range
		"No discount here":                       0,
		"Son 30 günde, %10 indirim, sepet hatır": 10,
	}
	for brief, want := range tests {
		if got := mockDiscount(brief); got != want {
			t.Errorf("mockDiscount(%q) = %d, want %d", brief, got, want)
		}
	}
}

// The case's example brief asks for a 30% discount reminder; the mock must
// carry the discount into every message, in the brief's language and style.
func TestMockContentMentionsDiscount(t *testing.T) {
	cart := segment.Rule{Match: "all", Conditions: []segment.Condition{{Event: "cart_add", WithinDays: 30, Exists: new(true)}}}
	tests := []struct {
		brief string
		rule  segment.Rule
		want  string
	}{
		{"Son 30 günde sepete ürün ekleyip satın almayan İstanbul kullanıcılarına %30 indirim hatırlatması", cart, "%30 indirim"},
		{"Kampanya: yüzde 15 indirim", segment.Rule{}, "%15 indirim"},
		{"Cart reminder with 30% discount", cart, "30% off"},
		{"Spring sale, 20% for everyone", segment.Rule{}, "20% off"},
	}
	for _, tt := range tests {
		c := mockContent(ContentInput{Brief: tt.brief, Channels: []string{"sms", "email"}, Rule: tt.rule})
		for name, text := range map[string]string{"sms": c.SMS.Body, "subject": c.Email.Subject, "body": c.Email.Body} {
			if !strings.Contains(text, tt.want) {
				t.Errorf("brief %q: %s = %q, want it to mention %q", tt.brief, name, text, tt.want)
			}
		}
	}
	if c := mockContent(ContentInput{Brief: "Sepet hatırlatması", Channels: []string{"sms"}, Rule: cart}); strings.Contains(c.SMS.Body, "%") {
		t.Errorf("no discount in the brief, but the SMS says %q", c.SMS.Body)
	}
}
