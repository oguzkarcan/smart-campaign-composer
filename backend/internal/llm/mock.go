package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

// Mock is a deterministic stand-in for Claude, used by default so the app and
// the tests run without an API key. It reads simple keywords from the brief
// (cities, cart, purchase, visits, a day count) in Turkish or English and
// writes fixed templates. The same input always gives the same output.
type Mock struct {
	// Delay is the pause between streamed chunks, so streaming is visible in
	// a demo. Zero in tests.
	Delay time.Duration
}

func (m *Mock) Model() string { return "mock" }

func (m *Mock) Complete(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	var out any
	switch in := req.Input.(type) {
	case SegmentInput:
		out = mockSegment(in)
	case ContentInput:
		out = mockContent(in)
	default:
		return Response{}, fmt.Errorf("mock: unsupported input %T", req.Input)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return Response{}, err
	}
	text := string(b)

	if onDelta != nil {
		for chunk := range chunks(text, 24) {
			if m.Delay > 0 {
				select {
				case <-ctx.Done():
					return Response{}, ctx.Err()
				case <-time.After(m.Delay):
				}
			}
			onDelta(chunk)
		}
	}
	return Response{
		Text:         text,
		InputTokens:  utf8.RuneCountInString(req.System) / 4,
		OutputTokens: utf8.RuneCountInString(text) / 4,
	}, nil
}

// chunks splits s into pieces of about n characters without breaking runes.
func chunks(s string, n int) func(func(string) bool) {
	return func(yield func(string) bool) {
		for len(s) > 0 {
			i, count := 0, 0
			for i < len(s) && count < n {
				_, size := utf8.DecodeRuneInString(s[i:])
				i += size
				count++
			}
			if !yield(s[:i]) {
				return
			}
			s = s[i:]
		}
	}
}

var dayCount = regexp.MustCompile(`(\d{1,3})\s*(gun|day)`)

func mockSegment(in SegmentInput) segment.Rule {
	brief := segment.Fold(in.Brief)
	rule := segment.Rule{Match: segment.MatchAll, Conditions: []segment.Condition{}}

	var cities segment.Values
	for _, c := range in.Catalog["city"] {
		if strings.Contains(brief, segment.Fold(c)) {
			cities = append(cities, c)
		}
	}
	switch len(cities) {
	case 0:
	case 1:
		rule.Conditions = append(rule.Conditions, segment.Condition{Field: "city", Op: segment.OpEq, Value: cities})
	default:
		rule.Conditions = append(rule.Conditions, segment.Condition{Field: "city", Op: segment.OpIn, Value: cities})
	}

	days := 30
	if m := dayCount.FindStringSubmatch(brief); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= segment.MaxWithinDays {
			days = n
		}
	}
	event := func(name string, exists bool) segment.Condition {
		return segment.Condition{Event: name, WithinDays: days, Exists: &exists}
	}

	if containsAny(brief, "cart", "sepet") {
		rule.Conditions = append(rule.Conditions, event("cart_add", true))
	}
	switch {
	case containsAny(brief, "satin almayan", "satin almamis", "almayan", "almamis", "did not purchase",
		"didn't purchase", "not purchased", "no purchase", "without purchas", "didn't buy", "did not buy"):
		rule.Conditions = append(rule.Conditions, event("purchase", false))
	case containsAny(brief, "satin alan", "satin almis", "purchased", "bought", "customers who buy"):
		rule.Conditions = append(rule.Conditions, event("purchase", true))
	}
	if containsAny(brief, "ziyaret", "goruntule", "visited", "viewed", "browsed") {
		rule.Conditions = append(rule.Conditions, event("page_view", true))
	}
	return rule
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// turkishHints are folded words that mark a brief as Turkish.
var turkishHints = []string{"kullanici", "musteri", "sepet", "satin", "gun", "icin", "olan", "kampanya"}

func mockContent(in ContentInput) content.Content {
	turkish := strings.ContainsAny(in.Brief, "çğıöşüÇĞİÖŞÜ") || containsAny(segment.Fold(in.Brief), turkishHints...)
	for _, c := range in.Rule.Conditions {
		if c.Field == "language" && c.Op == segment.OpEq && len(c.Value) == 1 {
			turkish = c.Value[0] == "tr"
		}
	}
	cart := false
	for _, c := range in.Rule.Conditions {
		cart = cart || c.Event == "cart_add"
	}

	// Short texts so they fit even with UnknownLength variables.
	var smsBody, subject, body string
	switch {
	case turkish && cart:
		smsBody = "{{first_name}}, sepetindeki ürünler seni bekliyor! Siparişini bugün tamamla."
		subject = "{{first_name}}, sepetini unutma!"
		body = "Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. Stoklar tükenmeden siparişini tamamla.\n\n{{city}} için teslimat seçeneklerini ödeme adımında görebilirsin."
	case turkish:
		smsBody = "{{first_name}}, sana özel yeni fırsatlar seni bekliyor! Hemen göz at."
		subject = "{{first_name}}, sana özel fırsatlar"
		body = "Merhaba {{first_name}},\n\nSenin için seçtiğimiz yeni fırsatlar yayında. {{city}} için teslimat seçeneklerini ödeme adımında görebilirsin."
	case cart:
		smsBody = "{{first_name}}, the items in your cart are waiting for you! Complete your order today."
		subject = "{{first_name}}, you left something behind"
		body = "Hi {{first_name}},\n\nThe items you added to your cart are still waiting. Complete your order before they sell out.\n\nYou can see delivery options for {{city}} at checkout."
	default:
		smsBody = "{{first_name}}, new offers picked for you are waiting! Take a look today."
		subject = "{{first_name}}, new offers picked for you"
		body = "Hi {{first_name}},\n\nWe picked new offers for you. You can see delivery options for {{city}} at checkout."
	}

	var c content.Content
	if slices.Contains(in.Channels, content.ChannelSMS) {
		c.SMS = &content.SMS{Body: smsBody}
	}
	if slices.Contains(in.Channels, content.ChannelEmail) {
		c.Email = &content.Email{Subject: subject, Body: body}
	}
	return c
}
