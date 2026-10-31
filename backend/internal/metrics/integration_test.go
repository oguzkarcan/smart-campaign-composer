//go:build integration

package metrics

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The queries run against the seeded database (make test-integration).
func TestIntegrationRead(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var messages, generations int64
	if err := db.QueryRow(ctx, "SELECT (SELECT count(*) FROM messages), (SELECT count(*) FROM llm_generations)").Scan(&messages, &generations); err != nil {
		t.Fatal(err)
	}
	s, err := Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var gotMessages, gotGenerations int64
	for _, n := range s.Messages {
		gotMessages += n
	}
	for _, g := range s.Generations {
		gotGenerations += g.Count
	}
	if gotMessages != messages || gotGenerations != generations {
		t.Fatalf("messages %d (want %d), generations %d (want %d)", gotMessages, messages, gotGenerations, generations)
	}

	var b strings.Builder
	if err := NewPG(db).WriteMetrics(ctx, &b); err != nil || !strings.Contains(b.String(), "# TYPE scc_provider_attempts_total counter") {
		t.Fatalf("err %v, output:\n%s", err, b.String())
	}
}
