package metrics

import (
	"strings"
	"testing"
)

func TestWriteText(t *testing.T) {
	s := Snapshot{
		Messages:  map[[2]string]int64{{"sms", "sent"}: 120, {"email", "retry"}: 3},
		Campaigns: map[string]int64{"sending": 1},
		Attempts: []Attempts{
			{Channel: "sms", Outcome: "sent", Code: "2xx", Count: 120, Seconds: 9.5},
			{Channel: "sms", Outcome: "retry", Code: "429", Count: 4, Seconds: 0.5},
		},
		Generations: []Generations{
			{Kind: "content", Result: "valid", Count: 2, InputTokens: 900, OutputTokens: 300, Seconds: 4},
			{Kind: "content", Result: "invalid", Count: 1, InputTokens: 450, OutputTokens: 100, Seconds: 2},
		},
	}
	var b strings.Builder
	if err := s.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"# TYPE scc_messages gauge\n",
		`scc_messages{channel="sms",status="sent"} 120` + "\n",
		`scc_messages{channel="email",status="retry"} 3` + "\n",
		`scc_messages{channel="email",status="failed"} 0` + "\n", // known series start at 0
		`scc_campaigns{status="sending"} 1` + "\n",
		"# TYPE scc_provider_attempts_total counter\n",
		`scc_provider_attempts_total{channel="sms",outcome="sent",code="2xx"} 120` + "\n",
		`scc_provider_attempts_total{channel="sms",outcome="retry",code="429"} 4` + "\n",
		`scc_provider_attempts_total{channel="email",outcome="permanent",code="4xx"} 0` + "\n",
		`scc_provider_attempt_duration_seconds_sum{channel="sms"} 10` + "\n",
		`scc_provider_attempt_duration_seconds_count{channel="sms"} 124` + "\n",
		`scc_llm_generations_total{kind="content",result="invalid"} 1` + "\n",
		`scc_llm_generations_total{kind="segment",result="error"} 0` + "\n",
		`scc_llm_tokens_total{kind="content",direction="input"} 1350` + "\n",
		`scc_llm_generation_duration_seconds_count{kind="content"} 3` + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}

	// Every line is a comment or "name{labels} value", and each metric has
	// exactly one HELP and TYPE.
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			seen[strings.Fields(line)[2]]++
			continue
		}
		if strings.HasPrefix(line, "# HELP ") {
			continue
		}
		if fields := strings.Fields(line); len(fields) != 2 || !strings.HasPrefix(fields[0], "scc_") {
			t.Errorf("malformed line %q", line)
		}
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("TYPE for %s written %d times", name, n)
		}
	}
}

func TestEscape(t *testing.T) {
	if got := escape("a\"b\\c\nd"); got != `a\"b\\c\nd` {
		t.Fatalf("escape = %s", got)
	}
}
