package segment

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Querier is the subset of *pgxpool.Pool used here.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Audience is how many users a rule matches and how many of them each channel
// may reach given their consent, in total and per user language.
type Audience struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
	// Languages has the same counts per language, for the languages that
	// matched anybody.
	Languages map[string]Counts `json:"languages,omitempty"`
}

// Counts are the audience numbers of one language.
type Counts struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
}

// Count runs a validated rule against the database.
func Count(ctx context.Context, db Querier, r Rule, now time.Time) (Audience, error) {
	sql, args, err := CountSQL(r, now)
	if err != nil {
		return Audience{}, err
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return Audience{}, fmt.Errorf("count audience: %w", err)
	}
	a := Audience{Languages: map[string]Counts{}}
	var lang string
	var c Counts
	_, err = pgx.ForEachRow(rows, []any{&lang, &c.Matched, &c.SMS, &c.Email}, func() error {
		a.Languages[lang] = c
		a.Matched += c.Matched
		a.SMS += c.SMS
		a.Email += c.Email
		return nil
	})
	if err != nil {
		return Audience{}, fmt.Errorf("count audience: %w", err)
	}
	return a, nil
}

// LoadCatalog reads the values that exist for each whitelisted field, so rules
// can only reference real cities and languages.
func LoadCatalog(ctx context.Context, db Querier) (Catalog, error) {
	cat := Catalog{}
	for name, col := range fields {
		rows, err := db.Query(ctx, "SELECT DISTINCT "+col+" FROM users u ORDER BY 1")
		if err != nil {
			return nil, fmt.Errorf("load %s values: %w", name, err)
		}
		values, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, fmt.Errorf("load %s values: %w", name, err)
		}
		cat[name] = values
	}
	return cat, nil
}
