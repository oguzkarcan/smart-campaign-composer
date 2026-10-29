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
		{"Customers who purchased in the last 90 days",
			`{"match": "all", "conditions": [{"event": "purchase", "within_days": 90, "exists": true}]}`},
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
	for _, brief := range []string{"Sepeti terk eden kullanıcılar", "Users who left their cart"} {
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
