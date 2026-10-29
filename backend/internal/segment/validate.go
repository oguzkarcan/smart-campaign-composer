package segment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Problem is one validation failure. Path points into the rule, e.g.
// "conditions[1].within_days", so the message can be fed back to the LLM.
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
	return "invalid segment rule: " + strings.Join(parts, "; ")
}

// Parse strictly decodes a rule, validates it against the catalog and returns
// it with values in canonical spelling. Errors are a ValidationError.
func Parse(data []byte, cat Catalog) (Rule, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var r Rule
	if err := dec.Decode(&r); err != nil {
		return Rule{}, ValidationError{{Message: "invalid JSON: " + err.Error()}}
	}
	if dec.More() {
		return Rule{}, ValidationError{{Message: "invalid JSON: unexpected data after the rule"}}
	}
	return r, Validate(&r, cat)
}

// Validate checks a rule and canonicalizes its values in place.
func Validate(r *Rule, cat Catalog) error {
	v := validator{cat: cat}
	v.group("", r.Match, r.Conditions, 1, true)
	if v.count > MaxConditions {
		v.add("", "too many conditions: %d, max %d", v.count, MaxConditions)
	}
	if len(v.problems) > 0 {
		return v.problems
	}
	return nil
}

type validator struct {
	cat      Catalog
	problems ValidationError
	count    int
}

func (v *validator) add(path, format string, args ...any) {
	v.problems = append(v.problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) group(prefix, match string, conds []Condition, depth int, root bool) {
	if match != MatchAll && match != MatchAny {
		v.add(prefix+"match", "must be %q or %q, got %q", MatchAll, MatchAny, match)
	}
	if !root && len(conds) == 0 {
		v.add(prefix+"conditions", "a nested group needs at least one condition")
	}
	for i := range conds {
		v.condition(fmt.Sprintf("%sconditions[%d]", prefix, i), &conds[i], depth)
	}
}

func (v *validator) condition(path string, c *Condition, depth int) {
	v.count++

	kinds := 0
	for _, set := range []bool{c.Field != "", c.Event != "", c.Match != ""} {
		if set {
			kinds++
		}
	}
	if kinds != 1 {
		v.add(path, "must have exactly one of \"field\", \"event\" or \"match\"")
		return
	}

	switch {
	case c.Field != "":
		v.field(path, c)
	case c.Event != "":
		v.event(path, c)
	default:
		v.unexpected(path, c.Op != "" || len(c.Value) > 0, "op and value")
		v.unexpected(path, c.WithinDays != 0 || c.Exists != nil || c.MinCount != 0, "within_days, exists and min_count")
		if depth >= MaxDepth {
			v.add(path, "groups are nested too deeply, max depth %d", MaxDepth)
			return
		}
		v.group(path+".", c.Match, c.Conditions, depth+1, false)
	}
}

func (v *validator) unexpected(path string, present bool, keys string) {
	if present {
		v.add(path, "%s are not allowed in this kind of condition", keys)
	}
}

func (v *validator) field(path string, c *Condition) {
	v.unexpected(path, c.WithinDays != 0 || c.Exists != nil || c.MinCount != 0, "within_days, exists and min_count")
	v.unexpected(path, len(c.Conditions) > 0, "nested conditions")

	if _, ok := fields[c.Field]; !ok {
		v.add(path+".field", "unknown field %q, allowed: %s", c.Field, keys(fields))
		return
	}
	if !ops[c.Op] {
		v.add(path+".op", "unknown op %q, allowed: %s", c.Op, keys(ops))
		return
	}

	switch n := len(c.Value); {
	case (c.Op == OpEq || c.Op == OpNeq) && n != 1:
		v.add(path+".value", "%q needs exactly one value, got %d", c.Op, n)
		return
	case n == 0 || n > MaxValues:
		v.add(path+".value", "%q needs 1-%d values, got %d", c.Op, MaxValues, n)
		return
	}

	allowed := v.cat[c.Field]
	canonical := make(Values, 0, len(c.Value))
	for _, val := range c.Value {
		match, ok := lookup(allowed, val)
		if !ok {
			v.add(path+".value", "unknown %s %q, allowed: %s", c.Field, val, strings.Join(allowed, ", "))
			continue
		}
		if !slices.Contains(canonical, match) {
			canonical = append(canonical, match)
		}
	}
	c.Value = canonical
}

func (v *validator) event(path string, c *Condition) {
	v.unexpected(path, c.Op != "" || len(c.Value) > 0, "op and value")
	v.unexpected(path, len(c.Conditions) > 0, "nested conditions")

	if !events[c.Event] {
		v.add(path+".event", "unknown event %q, allowed: %s", c.Event, keys(events))
	}
	if c.WithinDays < 1 || c.WithinDays > MaxWithinDays {
		v.add(path+".within_days", "must be between 1 and %d, got %d", MaxWithinDays, c.WithinDays)
	}
	if c.Exists == nil {
		v.add(path+".exists", "is required (true: did the event, false: did not)")
		return
	}
	if !*c.Exists && c.MinCount != 0 {
		v.add(path+".min_count", "is only allowed when exists is true")
	}
	if c.MinCount < 0 || c.MinCount > MaxMinCount {
		v.add(path+".min_count", "must be between 1 and %d, got %d", MaxMinCount, c.MinCount)
	}
}

// lookup finds val in allowed, ignoring case and Turkish diacritics, so
// "Istanbul", "istanbul" and "İSTANBUL" all resolve to "İstanbul".
func lookup(allowed []string, val string) (string, bool) {
	want := fold(val)
	for _, a := range allowed {
		if fold(a) == want {
			return a, true
		}
	}
	return "", false
}

var foldTurkish = strings.NewReplacer(
	"İ", "i", "I", "i", "ı", "i", "Ş", "s", "ş", "s", "Ğ", "g", "ğ", "g",
	"Ü", "u", "ü", "u", "Ö", "o", "ö", "o", "Ç", "c", "ç", "c",
)

func fold(s string) string {
	return strings.ToLower(foldTurkish.Replace(strings.TrimSpace(s)))
}

func keys[V any](m map[string]V) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
