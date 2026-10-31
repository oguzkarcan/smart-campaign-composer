package segment

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Where compiles a validated rule into a SQL boolean expression over the
// users table aliased as "u", plus its positional arguments. now anchors
// "within_days" windows, which keeps results reproducible in tests.
//
// It re-checks every name against the whitelists, so even an unvalidated rule
// cannot inject SQL; unknown names are an error, values are always arguments.
func Where(r Rule, now time.Time) (string, []any, error) {
	b := builder{now: now}
	if err := b.group(r.Match, r.Conditions); err != nil {
		return "", nil, err
	}
	return b.sb.String(), b.args, nil
}

// CountSQL counts matching users and how many of them each channel may reach,
// per user language. Columns: language, matched, sms, email.
func CountSQL(r Rule, now time.Time) (string, []any, error) {
	where, args, err := Where(r, now)
	if err != nil {
		return "", nil, err
	}
	return "SELECT u.language, count(*), " +
		"count(*) FILTER (WHERE " + consentColumns[ChannelSMS] + "), " +
		"count(*) FILTER (WHERE " + consentColumns[ChannelEmail] + ") " +
		"FROM users u WHERE " + where + " GROUP BY 1", args, nil
}

// RecipientsSQL selects the ids and languages of matching users who
// consented to channel.
// Consent is ANDed outside the rule's parentheses, so no rule (not even an
// "any" group) can bypass it.
func RecipientsSQL(r Rule, channel string, now time.Time) (string, []any, error) {
	consent, ok := consentColumns[channel]
	if !ok {
		return "", nil, fmt.Errorf("unknown channel %q", channel)
	}
	where, args, err := Where(r, now)
	if err != nil {
		return "", nil, err
	}
	return "SELECT u.id, u.language FROM users u WHERE (" + where + ") AND " + consent + " ORDER BY u.id", args, nil
}

// ReachableWhere matches users the rule selects who consented to at least one
// of channels, as a boolean expression over users aliased as "u". It is used
// to measure the audience a campaign can actually reach.
func ReachableWhere(r Rule, channels []string, now time.Time) (string, []any, error) {
	if len(channels) == 0 {
		return "", nil, fmt.Errorf("no channels")
	}
	consent := make([]string, len(channels))
	for i, ch := range channels {
		col, ok := consentColumns[ch]
		if !ok {
			return "", nil, fmt.Errorf("unknown channel %q", ch)
		}
		consent[i] = col
	}
	where, args, err := Where(r, now)
	if err != nil {
		return "", nil, err
	}
	return "(" + where + ") AND (" + strings.Join(consent, " OR ") + ")", args, nil
}

type builder struct {
	sb   strings.Builder
	args []any
	now  time.Time
}

func (b *builder) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *builder) group(match string, conds []Condition) error {
	var sep string
	switch match {
	case MatchAll:
		sep = " AND "
	case MatchAny:
		sep = " OR "
	default:
		return fmt.Errorf("unknown match %q", match)
	}
	if len(conds) == 0 {
		b.sb.WriteString("TRUE")
		return nil
	}
	b.sb.WriteString("(")
	for i, c := range conds {
		if i > 0 {
			b.sb.WriteString(sep)
		}
		if err := b.condition(c); err != nil {
			return err
		}
	}
	b.sb.WriteString(")")
	return nil
}

func (b *builder) condition(c Condition) error {
	switch {
	case c.Field != "":
		return b.field(c)
	case c.Event != "":
		return b.event(c)
	case c.Match != "":
		return b.group(c.Match, c.Conditions)
	default:
		return fmt.Errorf("empty condition")
	}
}

func (b *builder) field(c Condition) error {
	col, ok := fields[c.Field]
	if !ok {
		return fmt.Errorf("unknown field %q", c.Field)
	}
	if len(c.Value) == 0 {
		return fmt.Errorf("field %q has no value", c.Field)
	}
	values := []string(c.Value)
	switch c.Op {
	case OpEq:
		fmt.Fprintf(&b.sb, "%s = %s", col, b.arg(values[0]))
	case OpNeq:
		fmt.Fprintf(&b.sb, "%s <> %s", col, b.arg(values[0]))
	case OpIn:
		fmt.Fprintf(&b.sb, "%s = ANY(%s)", col, b.arg(values))
	case OpNotIn:
		fmt.Fprintf(&b.sb, "%s <> ALL(%s)", col, b.arg(values))
	default:
		return fmt.Errorf("unknown op %q", c.Op)
	}
	return nil
}

func (b *builder) event(c Condition) error {
	if !events[c.Event] {
		return fmt.Errorf("unknown event %q", c.Event)
	}
	if c.Exists == nil {
		return fmt.Errorf("event %q has no exists flag", c.Event)
	}
	since := b.now.Add(-time.Duration(c.WithinDays) * 24 * time.Hour)
	filter := fmt.Sprintf("e.user_id = u.id AND e.type = %s AND e.occurred_at >= %s", b.arg(c.Event), b.arg(since))

	switch {
	case !*c.Exists:
		fmt.Fprintf(&b.sb, "NOT EXISTS (SELECT 1 FROM user_events e WHERE %s)", filter)
	case c.MinCount > 1:
		fmt.Fprintf(&b.sb, "(SELECT count(*) FROM user_events e WHERE %s) >= %s", filter, b.arg(c.MinCount))
	default:
		fmt.Fprintf(&b.sb, "EXISTS (SELECT 1 FROM user_events e WHERE %s)", filter)
	}
	return nil
}
