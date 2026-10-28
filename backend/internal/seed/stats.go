package seed

import "time"

// Stats summarises generated data, logged after seeding and used in tests.
type Stats struct {
	Users       int
	Events      int
	ByLanguage  map[string]int
	ByConsent   map[string]int // "email+sms", "email", "sms", "none"
	ByEventType map[string]int
	// ExampleAudience counts users matching the case's example brief, before
	// consent filtering: in İstanbul, added to cart in the last 30 days and
	// did not purchase in the last 30 days.
	ExampleAudience int
}

func Summarize(users []User, now time.Time) Stats {
	s := Stats{
		Users:       len(users),
		ByLanguage:  map[string]int{},
		ByConsent:   map[string]int{},
		ByEventType: map[string]int{},
	}
	since := now.Add(-days(30))
	for _, u := range users {
		s.ByLanguage[u.Language]++
		s.ByConsent[consentKey(u)]++
		s.Events += len(u.Events)

		var cartRecent, purchaseRecent bool
		for _, e := range u.Events {
			s.ByEventType[e.Type]++
			if e.OccurredAt.Before(since) {
				continue
			}
			cartRecent = cartRecent || e.Type == EventCartAdd
			purchaseRecent = purchaseRecent || e.Type == EventPurchase
		}
		if u.City == "İstanbul" && cartRecent && !purchaseRecent {
			s.ExampleAudience++
		}
	}
	return s
}

func consentKey(u User) string {
	switch {
	case u.EmailOptIn && u.SMSOptIn:
		return "email+sms"
	case u.EmailOptIn:
		return "email"
	case u.SMSOptIn:
		return "sms"
	default:
		return "none"
	}
}
