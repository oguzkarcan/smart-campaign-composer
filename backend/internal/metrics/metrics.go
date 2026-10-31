// Package metrics exposes the system's state in the Prometheus text format.
// Everything is read from Postgres at scrape time: messages, provider calls
// and LLM generations are all rows there, so the API's /metrics covers the
// worker's work too without the two processes sharing memory.
package metrics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	channels        = []string{"sms", "email"}
	messageStatuses = []string{"queued", "processing", "retry", "sent", "failed"}
	campaignStatus  = []string{"draft", "audience_ready", "content_ready", "approved", "scheduled", "sending", "completed", "failed"}
)

// Attempts aggregates provider calls with the same labels.
type Attempts struct {
	Channel string
	Outcome string // sent, retry, permanent ("unknown" for rows without one)
	Code    string // 2xx, 4xx, 429, 5xx or none (no response)
	Count   int64
	Seconds float64 // total latency
}

// Generations aggregates LLM attempts with the same labels.
type Generations struct {
	Kind         string // segment, content
	Result       string // valid, invalid (failed validation), error (call failed)
	Count        int64
	InputTokens  int64
	OutputTokens int64
	Seconds      float64
}

// Snapshot is everything /metrics reports, read at one moment.
type Snapshot struct {
	Messages    map[[2]string]int64 // {channel, status} -> count
	Campaigns   map[string]int64    // status -> count
	Attempts    []Attempts
	Generations []Generations
}

// Read queries the snapshot. Each is one aggregate over its table; fine at
// this scale. With millions of messages these would become counters kept by
// the worker or a summary table.
func Read(ctx context.Context, db *pgxpool.Pool) (Snapshot, error) {
	s := Snapshot{Messages: map[[2]string]int64{}, Campaigns: map[string]int64{}}

	rows, err := db.Query(ctx, "SELECT channel, status, count(*) FROM messages GROUP BY 1, 2")
	if err != nil {
		return Snapshot{}, fmt.Errorf("read message counts: %w", err)
	}
	var ch, status string
	var n int64
	_, err = pgx.ForEachRow(rows, []any{&ch, &status, &n}, func() error { s.Messages[[2]string{ch, status}] = n; return nil })
	if err != nil {
		return Snapshot{}, fmt.Errorf("read message counts: %w", err)
	}

	rows, err = db.Query(ctx, "SELECT status, count(*) FROM campaigns GROUP BY 1")
	if err != nil {
		return Snapshot{}, fmt.Errorf("read campaign counts: %w", err)
	}
	_, err = pgx.ForEachRow(rows, []any{&status, &n}, func() error { s.Campaigns[status] = n; return nil })
	if err != nil {
		return Snapshot{}, fmt.Errorf("read campaign counts: %w", err)
	}

	rows, err = db.Query(ctx, `
		SELECT m.channel, coalesce(a.outcome, 'unknown'),
		       CASE WHEN a.http_status IS NULL THEN 'none'
		            WHEN a.http_status = 429 THEN '429'
		            ELSE (a.http_status / 100)::text || 'xx' END,
		       count(*), coalesce(sum(a.latency_ms), 0) / 1000.0
		FROM message_attempts a JOIN messages m ON m.id = a.message_id
		GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read provider attempts: %w", err)
	}
	s.Attempts, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Attempts])
	if err != nil {
		return Snapshot{}, fmt.Errorf("read provider attempts: %w", err)
	}

	rows, err = db.Query(ctx, `
		SELECT kind, CASE WHEN valid THEN 'valid' WHEN error IS NOT NULL THEN 'error' ELSE 'invalid' END,
		       count(*), coalesce(sum(input_tokens), 0), coalesce(sum(output_tokens), 0),
		       coalesce(sum(latency_ms), 0) / 1000.0
		FROM llm_generations GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read llm generations: %w", err)
	}
	s.Generations, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Generations])
	if err != nil {
		return Snapshot{}, fmt.Errorf("read llm generations: %w", err)
	}
	return s, nil
}

// PG serves metrics from the database.
type PG struct{ db *pgxpool.Pool }

func NewPG(db *pgxpool.Pool) *PG { return &PG{db: db} }

// WriteMetrics reads a snapshot and writes it in the text format.
func (p *PG) WriteMetrics(ctx context.Context, w io.Writer) error {
	s, err := Read(ctx, p.db)
	if err != nil {
		return err
	}
	return s.WriteText(w)
}

// WriteText writes the snapshot in the Prometheus text exposition format.
// Known label combinations are always present (as 0), so dashboards and
// rate() see a series before its first event.
//
// The *_total series are counts of stored rows, so they only drop if
// campaigns (and their rows) are deleted.
func (s Snapshot) WriteText(out io.Writer) error {
	w := bufio.NewWriter(out)

	header(w, "scc_messages", "gauge", "Messages by channel and status.")
	for _, ch := range channels {
		for _, st := range messageStatuses {
			sample(w, "scc_messages", s.Messages[[2]string{ch, st}], "channel", ch, "status", st)
		}
	}

	header(w, "scc_campaigns", "gauge", "Campaigns by status.")
	for _, st := range campaignStatus {
		sample(w, "scc_campaigns", s.Campaigns[st], "status", st)
	}

	header(w, "scc_provider_attempts_total", "counter",
		"Provider calls by channel, outcome (sent, retry, permanent) and response code class (none = no response).")
	attempts := map[[3]string]Attempts{}
	for _, a := range s.Attempts {
		attempts[[3]string{a.Channel, a.Outcome, a.Code}] = a
	}
	for _, ch := range channels {
		for _, base := range [][2]string{{"sent", "2xx"}, {"retry", "429"}, {"retry", "5xx"}, {"retry", "none"}, {"permanent", "4xx"}, {"permanent", "none"}} {
			if _, ok := attempts[[3]string{ch, base[0], base[1]}]; !ok {
				attempts[[3]string{ch, base[0], base[1]}] = Attempts{Channel: ch, Outcome: base[0], Code: base[1]}
			}
		}
	}
	keys := sortedKeys(attempts)
	for _, k := range keys {
		a := attempts[k]
		sample(w, "scc_provider_attempts_total", a.Count, "channel", a.Channel, "outcome", a.Outcome, "code", a.Code)
	}

	header(w, "scc_provider_attempt_duration_seconds", "summary", "Provider call latency by channel.")
	for _, ch := range channels {
		var count int64
		var secs float64
		for _, a := range s.Attempts {
			if a.Channel == ch {
				count += a.Count
				secs += a.Seconds
			}
		}
		sample(w, "scc_provider_attempt_duration_seconds_sum", secs, "channel", ch)
		sample(w, "scc_provider_attempt_duration_seconds_count", count, "channel", ch)
	}

	header(w, "scc_llm_generations_total", "counter",
		"LLM attempts by kind and result (valid, invalid = failed validation, error = call failed).")
	gens := map[[2]string]Generations{}
	for _, kind := range []string{"segment", "content"} {
		for _, result := range []string{"valid", "invalid", "error"} {
			gens[[2]string{kind, result}] = Generations{Kind: kind, Result: result}
		}
	}
	for _, g := range s.Generations {
		gens[[2]string{g.Kind, g.Result}] = g
	}
	genKeys := sortedKeys(gens)
	for _, k := range genKeys {
		sample(w, "scc_llm_generations_total", gens[k].Count, "kind", k[0], "result", k[1])
	}
	header(w, "scc_llm_tokens_total", "counter", "LLM tokens by kind and direction.")
	for _, kind := range []string{"segment", "content"} {
		var in, outTokens int64
		for _, g := range s.Generations {
			if g.Kind == kind {
				in += g.InputTokens
				outTokens += g.OutputTokens
			}
		}
		sample(w, "scc_llm_tokens_total", in, "kind", kind, "direction", "input")
		sample(w, "scc_llm_tokens_total", outTokens, "kind", kind, "direction", "output")
	}
	header(w, "scc_llm_generation_duration_seconds", "summary", "LLM call latency by kind.")
	for _, kind := range []string{"segment", "content"} {
		var count int64
		var secs float64
		for _, g := range s.Generations {
			if g.Kind == kind {
				count += g.Count
				secs += g.Seconds
			}
		}
		sample(w, "scc_llm_generation_duration_seconds_sum", secs, "kind", kind)
		sample(w, "scc_llm_generation_duration_seconds_count", count, "kind", kind)
	}
	return w.Flush()
}

func header(w *bufio.Writer, name, typ, help string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

// sample writes one line; labels are name/value pairs.
func sample[T int64 | float64](w *bufio.Writer, name string, v T, labels ...string) {
	w.WriteString(name)
	if len(labels) > 0 {
		w.WriteByte('{')
		for i := 0; i < len(labels); i += 2 {
			if i > 0 {
				w.WriteByte(',')
			}
			fmt.Fprintf(w, "%s=\"%s\"", labels[i], escape(labels[i+1]))
		}
		w.WriteByte('}')
	}
	fmt.Fprintf(w, " %v\n", v)
}

// escape quotes a label value as the text format requires.
func escape(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func sortedKeys[K [2]string | [3]string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	return keys
}
