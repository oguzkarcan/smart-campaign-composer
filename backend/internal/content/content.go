// Package content parses and validates campaign message content produced by
// Claude or edited by the marketer, and renders it per recipient.
//
// Length limits are checked on the worst case after variable expansion: every
// variable counts as the longest value it can take in the target audience.
package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	ChannelSMS   = "sms"
	ChannelEmail = "email"

	MaxSMSLength       = 160
	MaxSubjectLength   = 80
	MaxEmailBodyLength = 5000
)

// Limits are the maximum worst-case lengths, keyed by the same paths that
// validation problems use.
var Limits = map[string]int{
	"sms.body":      MaxSMSLength,
	"email.subject": MaxSubjectLength,
	"email.body":    MaxEmailBodyLength,
}

// Content holds one message per channel. Only the campaign's channels may be
// set; the others must be nil.
type Content struct {
	SMS   *SMS   `json:"sms,omitempty"`
	Email *Email `json:"email,omitempty"`
}

type SMS struct {
	Body string `json:"body"`
}

type Email struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Problem is one validation failure. Path points into the content, e.g.
// "sms.body", so the message can be fed back to the LLM.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError lists every problem found, not just the first, so a single
// repair round can fix them all.
type ValidationError []Problem

func (e ValidationError) Error() string {
	parts := make([]string, len(e))
	for i, p := range e {
		if p.Path == "" {
			parts[i] = p.Message
		} else {
			parts[i] = p.Path + ": " + p.Message
		}
	}
	return "invalid content: " + strings.Join(parts, "; ")
}

// Parse strictly decodes content, validates it for the given channels and
// returns it with variables in canonical spelling. Errors are a
// ValidationError.
func Parse(data []byte, channels []string, lens MaxLengths) (Content, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Content
	if err := dec.Decode(&c); err != nil {
		return Content{}, ValidationError{{Message: "invalid JSON: " + err.Error()}}
	}
	// Decoding a second value must hit EOF. dec.More() is not enough: it
	// reports false for a stray "}" or "]", which ExtractJSON keeps when the
	// model's answer ends with one.
	if err := dec.Decode(&json.RawMessage{}); err != io.EOF {
		return Content{}, ValidationError{{Message: "invalid JSON: unexpected data after the content"}}
	}
	return c, Validate(&c, channels, lens)
}

// Validate checks content against the campaign's channels and the worst-case
// length limits, and canonicalizes variables in place. It is used for both
// generated and marketer-edited content.
func Validate(c *Content, channels []string, lens MaxLengths) error {
	v := validator{lens: lens}
	want := map[string]bool{}
	for _, ch := range channels {
		want[ch] = true
	}

	switch {
	case want[ChannelSMS] && c.SMS == nil:
		v.add(ChannelSMS, "is required for this campaign")
	case !want[ChannelSMS] && c.SMS != nil:
		v.add(ChannelSMS, "is not a channel of this campaign, remove it")
	case c.SMS != nil:
		c.SMS.Body = v.text("sms.body", c.SMS.Body, MaxSMSLength)
	}

	switch {
	case want[ChannelEmail] && c.Email == nil:
		v.add(ChannelEmail, "is required for this campaign")
	case !want[ChannelEmail] && c.Email != nil:
		v.add(ChannelEmail, "is not a channel of this campaign, remove it")
	case c.Email != nil:
		c.Email.Subject = v.text("email.subject", c.Email.Subject, MaxSubjectLength)
		if strings.ContainsAny(c.Email.Subject, "\r\n") {
			v.add("email.subject", "must be a single line")
		}
		c.Email.Body = v.text("email.body", c.Email.Body, MaxEmailBodyLength)
	}

	if len(v.problems) > 0 {
		return v.problems
	}
	return nil
}

type validator struct {
	lens     MaxLengths
	problems ValidationError
}

func (v *validator) add(path, format string, args ...any) {
	v.problems = append(v.problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

// text validates one template and returns it trimmed, with variables in
// canonical spelling.
func (v *validator) text(path, s string, limit int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		v.add(path, "must not be empty")
		return s
	}
	if !utf8.ValidString(s) {
		v.add(path, "must be valid UTF-8")
		return s
	}

	canonical, problems := canonicalize(s)
	for _, p := range problems {
		v.add(path, "%s", p)
	}
	if len(problems) > 0 {
		return s
	}

	if n := v.lens.WorstCase(canonical); n > limit {
		v.add(path, "is %d characters in the worst case, max %d; shorten it by at least %d%s",
			n, limit, n-limit, v.lens.explain(canonical))
	}
	return canonical
}
