// Package segment defines the audience rule DSL that Claude produces, validates
// it, and compiles it to parameterized SQL.
//
// LLM output is never executed as SQL. Column names come only from the
// whitelists in this file; every value from a rule becomes a query parameter.
package segment

import (
	"encoding/json"
	"errors"
)

const (
	MatchAll = "all"
	MatchAny = "any"

	OpEq    = "eq"
	OpNeq   = "neq"
	OpIn    = "in"
	OpNotIn = "not_in"

	ChannelSMS   = "sms"
	ChannelEmail = "email"

	MaxDepth      = 3
	MaxConditions = 20
	MaxValues     = 50
	MaxWithinDays = 365
	MaxMinCount   = 100
)

// Rule is the root of a segment: a group of conditions combined with "all"
// (AND) or "any" (OR). A root with no conditions matches every user.
type Rule struct {
	Match      string      `json:"match"`
	Conditions []Condition `json:"conditions"`
}

// Condition is exactly one of three kinds, decided by which key is set:
//
//	field: {"field": "city", "op": "in", "value": ["İstanbul", "Ankara"]}
//	event: {"event": "cart_add", "within_days": 30, "exists": true, "min_count": 2}
//	group: {"match": "any", "conditions": [...]}
type Condition struct {
	Field string `json:"field,omitempty"`
	Op    string `json:"op,omitempty"`
	Value Values `json:"value,omitempty"`

	Event      string `json:"event,omitempty"`
	WithinDays int    `json:"within_days,omitempty"`
	Exists     *bool  `json:"exists,omitempty"`
	MinCount   int    `json:"min_count,omitempty"`

	Match      string      `json:"match,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// Values accepts a single string or an array of strings.
type Values []string

func (v *Values) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*v = Values{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("value must be a string or an array of strings")
	}
	*v = many
	return nil
}

// fields maps DSL field names to SQL columns. Nothing else can reach SQL.
var fields = map[string]string{
	"city":     "u.city",
	"language": "u.language",
}

var ops = map[string]bool{OpEq: true, OpNeq: true, OpIn: true, OpNotIn: true}

var events = map[string]bool{"page_view": true, "cart_add": true, "purchase": true}

// consentColumns maps channels to the opt-in column that must be true.
var consentColumns = map[string]string{
	ChannelSMS:   "u.sms_opt_in",
	ChannelEmail: "u.email_opt_in",
}

// Catalog lists the allowed values per field in their canonical spelling,
// e.g. {"city": ["Ankara", "İstanbul", ...], "language": ["en", "tr"]}.
type Catalog map[string][]string
