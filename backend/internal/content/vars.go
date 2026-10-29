package content

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	VarFirstName = "first_name"
	VarCity      = "city"

	// UnknownLength is the worst-case length assumed for a variable missing
	// from MaxLengths or measured as 0. It is deliberately generous so a missing measurement
	// can only make validation stricter, never looser.
	UnknownLength = 40
)

// Variables lists the personalization variables a template may use.
var Variables = []string{VarFirstName, VarCity}

var (
	// varToken matches "{{ name }}" with any spacing; the name is checked
	// against Variables separately so unknown names get a clear error.
	varToken = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
	// singleBrace catches the common "{first_name}" mistake.
	singleBrace = regexp.MustCompile(`\{\s*(` + strings.Join(Variables, "|") + `)\s*\}`)
)

func token(name string) string { return "{{" + name + "}}" }

func allowed(name string) bool {
	for _, v := range Variables {
		if v == name {
			return true
		}
	}
	return false
}

// canonicalize rewrites "{{ first_name }}" to "{{first_name}}" and reports
// unknown or malformed variables.
func canonicalize(s string) (string, []string) {
	var problems []string
	out := varToken.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.TrimSpace(m[2 : len(m)-2])
		if !allowed(name) {
			problems = append(problems, fmt.Sprintf("unknown variable %q, allowed: %s", m, allowedList()))
			return m
		}
		return token(name)
	})
	if len(problems) > 0 {
		return s, problems
	}

	// Braces outside variables are almost always a broken variable, and would
	// reach recipients verbatim, so they are rejected.
	rest := varToken.ReplaceAllString(out, "")
	if m := singleBrace.FindString(rest); m != "" {
		problems = append(problems, fmt.Sprintf("%q uses single braces; write variables like %s", m, token(VarFirstName)))
	} else if strings.ContainsAny(rest, "{}") {
		problems = append(problems, fmt.Sprintf("has stray \"{\" or \"}\"; braces are only allowed in variables like %s", token(VarFirstName)))
	}
	return out, problems
}

func allowedList() string {
	out := make([]string, len(Variables))
	for i, v := range Variables {
		out[i] = token(v)
	}
	return strings.Join(out, ", ")
}

// MaxLengths is the longest value, in characters, each variable takes in the
// target audience, e.g. {"first_name": 14, "city": 10}.
type MaxLengths map[string]int

// of returns how many characters a variable counts as. A missing or zero
// measurement (e.g. an empty audience) counts as UnknownLength. A variable is
// never counted shorter than its placeholder, so the worst case also covers
// the raw template, which the database checks as stored.
func (m MaxLengths) of(name string) int {
	n := m[name]
	if n <= 0 {
		n = UnknownLength
	}
	return max(n, utf8.RuneCountInString(token(name)))
}

// Counted returns how many characters each variable counts as in WorstCase,
// so a UI can show the same worst-case length the server validates.
func (m MaxLengths) Counted() map[string]int {
	out := make(map[string]int, len(Variables))
	for _, name := range Variables {
		out[name] = m.of(name)
	}
	return out
}

// WorstCase returns the length of a canonical template, in characters, with
// every variable replaced by its longest possible value.
func (m MaxLengths) WorstCase(tmpl string) int {
	n := utf8.RuneCountInString(tmpl)
	for _, loc := range varToken.FindAllStringSubmatchIndex(tmpl, -1) {
		name := tmpl[loc[2]:loc[3]]
		n += m.of(name) - utf8.RuneCountInString(tmpl[loc[0]:loc[1]])
	}
	return n
}

// explain says how the variables used in tmpl were counted, so a repair
// prompt knows where the extra characters come from.
func (m MaxLengths) explain(tmpl string) string {
	used := map[string]bool{}
	for _, sub := range varToken.FindAllStringSubmatch(tmpl, -1) {
		used[sub[1]] = true
	}
	if len(used) == 0 {
		return ""
	}
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("%s counted as %d", token(name), m.of(name))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// Render fills a validated template for one recipient. Unknown variables
// cannot occur in validated content; any that do are left untouched.
func Render(tmpl string, vars map[string]string) string {
	return varToken.ReplaceAllStringFunc(tmpl, func(m string) string {
		if v, ok := vars[strings.TrimSpace(m[2:len(m)-2])]; ok {
			return strings.TrimSpace(v)
		}
		return m
	})
}
