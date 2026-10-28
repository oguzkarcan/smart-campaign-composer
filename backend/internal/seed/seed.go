// Package seed generates deterministic fake users and behavioural events.
//
// Generation is pure: the same Config always produces the same data, with all
// timestamps relative to Config.Now, so "last 30 days" rules keep matching no
// matter when the seed runs.
package seed

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

const (
	EventPageView = "page_view"
	EventCartAdd  = "cart_add"
	EventPurchase = "purchase"

	// HistoryDays is how far back events go.
	HistoryDays = 90
)

// Behaviour tuning. Roughly a quarter of users are inactive; the rest browse in
// sessions, some sessions add to cart, and fewer than half of carts convert.
const (
	pInactive       = 0.25
	pCasual         = 0.50 // 1-4 sessions; the remainder are active (5-15)
	pCartPerSession = 0.35
	pPurchaseOnCart = 0.45
	pEnglish        = 0.20
	pEmailOptIn     = 0.70
	pSMSOptIn       = 0.55
)

type Config struct {
	Users int
	Seed  uint64
	Now   time.Time
}

type User struct {
	FirstName  string
	LastName   string
	Email      string
	Phone      string
	City       string
	Language   string
	EmailOptIn bool
	SMSOptIn   bool
	CreatedAt  time.Time
	Events     []Event
}

type Event struct {
	Type       string
	Amount     *float64 // nil for page views
	OccurredAt time.Time
}

// Generate returns cfg.Users users with their events, ordered by occurrence.
func Generate(cfg Config) []User {
	r := rand.New(rand.NewPCG(cfg.Seed, 0x5eed))
	users := make([]User, cfg.Users)
	for i := range users {
		users[i] = genUser(r, i, cfg.Now)
	}
	return users
}

func genUser(r *rand.Rand, i int, now time.Time) User {
	lang, firsts, lasts := "tr", trFirstNames, trLastNames
	if r.Float64() < pEnglish {
		lang, firsts, lasts = "en", enFirstNames, enLastNames
	}
	first, last := pick(r, firsts), pick(r, lasts)

	return User{
		FirstName: first,
		LastName:  last,
		// The index suffix keeps emails and phones unique.
		Email:      fmt.Sprintf("%s.%s.%d@example.com", slug(first), slug(last), i+1),
		Phone:      fmt.Sprintf("+90%d%07d", 530+r.IntN(30), i+1),
		City:       pickWeighted(r, cities),
		Language:   lang,
		EmailOptIn: r.Float64() < pEmailOptIn,
		SMSOptIn:   r.Float64() < pSMSOptIn,
		// Accounts predate the event history.
		CreatedAt: now.Add(-days(HistoryDays) - randDuration(r, days(640))),
		Events:    genEvents(r, now),
	}
}

func genEvents(r *rand.Rand, now time.Time) []Event {
	var sessions int
	switch p := r.Float64(); {
	case p < pInactive:
		return nil
	case p < pInactive+pCasual:
		sessions = 1 + r.IntN(4)
	default:
		sessions = 5 + r.IntN(11)
	}

	var events []Event
	for range sessions {
		// Start at least an hour ago so the session's page views stay in the past.
		at := now.Add(-time.Hour - randDuration(r, days(HistoryDays)-time.Hour))
		for range 1 + r.IntN(5) {
			events = append(events, Event{Type: EventPageView, OccurredAt: at})
			at = at.Add(time.Minute + randDuration(r, 10*time.Minute))
		}
		if r.Float64() >= pCartPerSession {
			continue
		}
		price := randPrice(r)
		events = append(events, Event{Type: EventCartAdd, Amount: &price, OccurredAt: at})
		if r.Float64() >= pPurchaseOnCart {
			continue
		}
		// A purchase that would land in the future simply hasn't happened yet.
		if bought := at.Add(randDuration(r, 48*time.Hour)); !bought.After(now) {
			paid := price
			events = append(events, Event{Type: EventPurchase, Amount: &paid, OccurredAt: bought})
		}
	}

	slices.SortStableFunc(events, func(a, b Event) int { return a.OccurredAt.Compare(b.OccurredAt) })
	return events
}

// randPrice returns a cart value between 50.00 and 2000.99.
func randPrice(r *rand.Rand) float64 {
	return math.Round((50+float64(r.IntN(1951))+float64(r.IntN(100))/100)*100) / 100
}

func pick(r *rand.Rand, values []string) string {
	return values[r.IntN(len(values))]
}

func pickWeighted(r *rand.Rand, values []weighted) string {
	total := 0
	for _, v := range values {
		total += v.weight
	}
	n := r.IntN(total)
	for _, v := range values {
		if n < v.weight {
			return v.value
		}
		n -= v.weight
	}
	return values[len(values)-1].value
}

func randDuration(r *rand.Rand, max time.Duration) time.Duration {
	return time.Duration(r.Int64N(int64(max)))
}

func days(n int) time.Duration {
	return time.Duration(n) * 24 * time.Hour
}

// slug ASCII-folds a name for use in an email address.
var foldTurkish = strings.NewReplacer(
	"ç", "c", "Ç", "c", "ğ", "g", "Ğ", "g", "ı", "i", "İ", "i",
	"ö", "o", "Ö", "o", "ş", "s", "Ş", "s", "ü", "u", "Ü", "u", " ", "-",
)

func slug(s string) string {
	return strings.ToLower(foldTurkish.Replace(s))
}
