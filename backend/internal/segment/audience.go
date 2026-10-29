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
// may reach given their consent.
type Audience struct {
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
	var a Audience
	if err := db.QueryRow(ctx, sql, args...).Scan(&a.Matched, &a.SMS, &a.Email); err != nil {
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
