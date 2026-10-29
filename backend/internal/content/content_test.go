package content

import (
	"errors"
	"strings"
	"testing"
)

var (
	both      = []string{ChannelSMS, ChannelEmail}
	smsOnly   = []string{ChannelSMS}
	emailOnly = []string{ChannelEmail}

	// testLens is what the seeded İstanbul audience could look like: the
	// longest first name is a compound one, longer than its placeholder.
	testLens = MaxLengths{VarFirstName: 15, VarCity: 8}
)

const exampleContent = `{
	"sms": {"body": "{{first_name}}, sepetindeki ürünler seni bekliyor! Bugün %10 indirimle tamamla."},
	"email": {"subject": "{{first_name}}, sepetini unuttun mu?", "body": "Merhaba {{first_name}},\n\n{{city}} için ücretsiz kargo seni bekliyor."}
}`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(exampleContent), both, testLens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.SMS == nil || c.Email == nil {
		t.Fatalf("content = %+v, want both channels", c)
	}
	if !strings.HasPrefix(c.Email.Subject, "{{first_name}},") {
		t.Fatalf("subject = %q", c.Email.Subject)
	}
}

func TestParseCanonicalizes(t *testing.T) {
	c, err := Parse([]byte(`{"sms": {"body": "  Hi {{ first_name }}, see you in {{city }}!  "}}`), smsOnly, testLens)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Hi {{first_name}}, see you in {{city}}!"; c.SMS.Body != want {
		t.Fatalf("body = %q, want %q", c.SMS.Body, want)
	}
}

func TestParseRejects(t *testing.T) {
	long := strings.Repeat("a", 150)
	tests := []struct {
		name     string
		raw      string
		channels []string
		path     string
		contains string
	}{
		{"not JSON", `sms: hi`, smsOnly, "", "invalid JSON"},
		{"trailing data", `{"sms": {"body": "hi"}} {}`, smsOnly, "", "unexpected data"},
		{"unknown key", `{"sms": {"body": "hi", "sql": "DROP TABLE users"}}`, smsOnly, "", "unknown field"},
		{"unknown channel key", `{"sms": {"body": "hi"}, "push": {"body": "hi"}}`, smsOnly, "", "unknown field"},
		{"sms subject", `{"sms": {"body": "hi", "subject": "x"}}`, smsOnly, "", "unknown field"},
		{"missing channel", `{"sms": {"body": "hi"}}`, both, "email", "required"},
		{"extra channel", `{"sms": {"body": "hi"}, "email": {"subject": "s", "body": "b"}}`, smsOnly, "email", "not a channel"},
		{"empty body", `{"sms": {"body": "   "}}`, smsOnly, "sms.body", "must not be empty"},
		{"empty subject", `{"email": {"subject": "", "body": "b"}}`, emailOnly, "email.subject", "must not be empty"},
		{"multi-line subject", `{"email": {"subject": "a\nb", "body": "b"}}`, emailOnly, "email.subject", "single line"},
		{"carriage return in subject", `{"email": {"subject": "a\rb", "body": "b"}}`, emailOnly, "email.subject", "single line"},
		{"unknown variable", `{"sms": {"body": "Hi {{last_name}}"}}`, smsOnly, "sms.body", `unknown variable "{{last_name}}"`},
		{"template injection", `{"sms": {"body": "Hi {{.Phone}}"}}`, smsOnly, "sms.body", "unknown variable"},
		{"single braces", `{"sms": {"body": "Hi {first_name}"}}`, smsOnly, "sms.body", "single braces"},
		{"unbalanced", `{"sms": {"body": "Hi {{first_name}"}}`, smsOnly, "sms.body", "braces"},
		{"stray brace", `{"sms": {"body": "Hi } there"}}`, smsOnly, "sms.body", "stray"},
		{"sms too long", `{"sms": {"body": "` + long + `{{first_name}}"}}`, smsOnly, "sms.body", "165 characters in the worst case, max 160; shorten it by at least 5 ({{first_name}} counted as 15)"},
		{"subject too long", `{"email": {"subject": "` + long[:75] + ` {{city}}", "body": "b"}}`, emailOnly, "email.subject", "84 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.raw), tt.channels, testLens)
			var verr ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			for _, p := range verr {
				if p.Path == tt.path && strings.Contains(p.Message, tt.contains) {
					return
				}
			}
			t.Fatalf("no problem at %q containing %q in: %v", tt.path, tt.contains, err)
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	c := Content{
		SMS:   &SMS{Body: "Hi {{name}}"},
		Email: &Email{Subject: "", Body: "{x}"},
	}
	err := Validate(&c, both, testLens)
	var verr ValidationError
	if !errors.As(err, &verr) || len(verr) != 3 {
		t.Fatalf("err = %v, want 3 problems", err)
	}
}

func TestWorstCase(t *testing.T) {
	tests := []struct {
		tmpl string
		lens MaxLengths
		want int
	}{
		{"Merhaba", testLens, 7},
		{"Şükran İğdır", testLens, 12}, // characters, not bytes
		{"{{first_name}}", testLens, 15},
		{"{{first_name}} {{first_name}}, {{city}}", testLens, 15 + 1 + 15 + 2 + 8},
		{"{{city}}", MaxLengths{}, UnknownLength},                // unmeasured variables are pessimistic
		{"{{city}}", MaxLengths{VarCity: 0}, UnknownLength},      // so is a zero (empty audience)
		{"{{first_name}}", MaxLengths{VarFirstName: 3}, 14},      // never shorter than the placeholder
		{"{{city}} {{city}}", MaxLengths{VarCity: 4}, 8 + 1 + 8}, // per occurrence
	}
	for _, tt := range tests {
		if got := tt.lens.WorstCase(tt.tmpl); got != tt.want {
			t.Errorf("WorstCase(%q) = %d, want %d", tt.tmpl, got, tt.want)
		}
	}
}

func TestLimitIsInclusive(t *testing.T) {
	body := strings.Repeat("a", MaxSMSLength-15) + "{{first_name}}"
	if err := Validate(&Content{SMS: &SMS{Body: body}}, smsOnly, testLens); err != nil {
		t.Fatalf("exactly 160 characters in the worst case should pass: %v", err)
	}
}

func TestRender(t *testing.T) {
	vars := map[string]string{VarFirstName: "Muhammed Emin", VarCity: "İstanbul"}
	got := Render("Merhaba {{first_name}}, {{city}} için {{first_name}}!", vars)
	if want := "Merhaba Muhammed Emin, İstanbul için Muhammed Emin!"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

// A rendered message never exceeds the limit that validation promised, as
// long as every value is within MaxLengths.
func TestRenderedFitsWorstCase(t *testing.T) {
	c, err := Parse([]byte(exampleContent), both, testLens)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{VarFirstName: strings.Repeat("Ş", 15), VarCity: strings.Repeat("İ", 8)}
	for tmpl, limit := range map[string]int{c.SMS.Body: MaxSMSLength, c.Email.Subject: MaxSubjectLength} {
		if n := len([]rune(Render(tmpl, vars))); n > limit || n != testLens.WorstCase(tmpl) {
			t.Errorf("rendered %q is %d characters, worst case %d, limit %d", tmpl, n, testLens.WorstCase(tmpl), limit)
		}
	}
}

// The database checks char_length(body) <= 160 on the raw template. With short
// names the expanded message would fit, but the stored template would not, so
// validation must reject it rather than let the insert fail.
func TestRawTemplateMustFitWhenNamesAreShort(t *testing.T) {
	short := MaxLengths{VarFirstName: 4, VarCity: 5}
	body := strings.Repeat("a", 150) + "{{first_name}}" // raw 164, expanded 154
	err := Validate(&Content{SMS: &SMS{Body: body}}, smsOnly, short)
	if err == nil || !strings.Contains(err.Error(), "164 characters") {
		t.Fatalf("err = %v, want the 164-character raw template rejected", err)
	}
}

func TestCounted(t *testing.T) {
	got := MaxLengths{VarFirstName: 15, VarCity: 3}.Counted()
	if got[VarFirstName] != 15 || got[VarCity] != 8 || len(got) != len(Variables) {
		t.Fatalf("Counted = %v, want first_name 15 and city 8 (its placeholder)", got)
	}
	if got := (MaxLengths{}).Counted(); got[VarCity] != UnknownLength {
		t.Fatalf("unmeasured Counted = %v", got)
	}
}
