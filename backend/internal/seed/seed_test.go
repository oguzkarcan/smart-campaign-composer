package seed

import (
	"reflect"
	"testing"
	"time"
	"unicode/utf8"
)

var testNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func generate(t *testing.T, seed uint64) []User {
	t.Helper()
	return Generate(Config{Users: 5000, Seed: seed, Now: testNow})
}

func TestGenerateIsDeterministic(t *testing.T) {
	a, b := generate(t, 42), generate(t, 42)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same config produced different data")
	}
	if reflect.DeepEqual(a, generate(t, 7)) {
		t.Fatal("different seeds produced identical data")
	}
}

func TestUsersAreValid(t *testing.T) {
	users := generate(t, 42)
	emails, phones := map[string]bool{}, map[string]bool{}
	nonASCII := 0

	for _, u := range users {
		if emails[u.Email] || phones[u.Phone] {
			t.Fatalf("duplicate email or phone: %s %s", u.Email, u.Phone)
		}
		emails[u.Email], phones[u.Phone] = true, true

		if !isASCII(u.Email) {
			t.Errorf("email is not ASCII: %q", u.Email)
		}
		if u.Language != "tr" && u.Language != "en" {
			t.Errorf("unexpected language %q", u.Language)
		}
		if u.CreatedAt.After(testNow.Add(-days(HistoryDays))) {
			t.Errorf("user created inside the event history window: %v", u.CreatedAt)
		}
		if !isASCII(u.FirstName) {
			nonASCII++
		}
	}
	if nonASCII == 0 {
		t.Error("expected some Turkish names with diacritics")
	}
}

func TestEventsAreValid(t *testing.T) {
	oldest := testNow.Add(-days(HistoryDays))
	for _, u := range generate(t, 42) {
		for i, e := range u.Events {
			if e.OccurredAt.Before(oldest) || e.OccurredAt.After(testNow) {
				t.Fatalf("event outside history window: %v", e.OccurredAt)
			}
			if i > 0 && e.OccurredAt.Before(u.Events[i-1].OccurredAt) {
				t.Fatal("events are not sorted by time")
			}
			switch e.Type {
			case EventPageView:
				if e.Amount != nil {
					t.Fatal("page view has an amount")
				}
			case EventCartAdd, EventPurchase:
				if e.Amount == nil || *e.Amount < 50 || *e.Amount > 2001 {
					t.Fatalf("%s has invalid amount %v", e.Type, e.Amount)
				}
			default:
				t.Fatalf("unknown event type %q", e.Type)
			}
		}
	}
}

func TestDistribution(t *testing.T) {
	s := Summarize(generate(t, 42), testNow)

	for _, k := range []string{"email+sms", "email", "sms", "none"} {
		if s.ByConsent[k] == 0 {
			t.Errorf("no users with consent %q", k)
		}
	}
	if share := float64(s.ByLanguage["en"]) / float64(s.Users); share < 0.15 || share > 0.25 {
		t.Errorf("English share = %.2f, want about 0.20", share)
	}
	for _, typ := range []string{EventPageView, EventCartAdd, EventPurchase} {
		if s.ByEventType[typ] == 0 {
			t.Errorf("no %s events", typ)
		}
	}
	if s.ByEventType[EventPurchase] >= s.ByEventType[EventCartAdd] {
		t.Error("expected fewer purchases than cart adds")
	}
	// The example brief should match a meaningful, but not huge, audience.
	if s.ExampleAudience < 150 || s.ExampleAudience > 1000 {
		t.Errorf("example brief audience = %d, want 150-1000", s.ExampleAudience)
	}
}

func TestSlug(t *testing.T) {
	tests := map[string]string{
		"Ayşe Nur": "ayse-nur",
		"İbrahim":  "ibrahim",
		"Çağrı":    "cagri",
		"Öztürk":   "ozturk",
		"Liam":     "liam",
	}
	for in, want := range tests {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func isASCII(s string) bool {
	return utf8.RuneCountInString(s) == len(s)
}
