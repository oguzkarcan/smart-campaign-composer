package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/segment"
)

// PGStore is the Postgres Store.
type PGStore struct {
	db *pgxpool.Pool
}

func NewPGStore(db *pgxpool.Pool) *PGStore { return &PGStore{db: db} }

// editableSQL guards every pre-approval write, so a write that races with an
// approval affects no rows instead of changing approved content.
const editableSQL = "status IN ('draft', 'audience_ready', 'content_ready')"

func (s *PGStore) Create(ctx context.Context, brief string, channels []string) (Campaign, error) {
	var id int64
	err := s.db.QueryRow(ctx, "INSERT INTO campaigns (brief, channels) VALUES ($1, $2) RETURNING id", brief, channels).Scan(&id)
	if err != nil {
		return Campaign{}, fmt.Errorf("create campaign: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *PGStore) Get(ctx context.Context, id int64) (Campaign, error) {
	var (
		c                   Campaign
		rule                []byte
		matched, sms, email *int
		lastError           *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, brief, status, channels, segment_rule, audience_count, audience_sms, audience_email,
		       last_error, approved_at, created_at, updated_at
		FROM campaigns WHERE id = $1`, id).Scan(
		&c.ID, &c.Brief, &c.Status, &c.Channels, &rule, &matched, &sms, &email,
		&lastError, &c.ApprovedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	if err != nil {
		return Campaign{}, fmt.Errorf("get campaign %d: %w", id, err)
	}
	if rule != nil {
		c.Rule = &segment.Rule{}
		if err := json.Unmarshal(rule, c.Rule); err != nil {
			return Campaign{}, fmt.Errorf("decode segment of campaign %d: %w", id, err)
		}
	}
	if matched != nil {
		c.Audience = &segment.Audience{Matched: *matched, SMS: deref(sms), Email: deref(email)}
	}
	if lastError != nil {
		c.LastError = *lastError
	}

	rows, err := s.db.Query(ctx, `
		SELECT channel, coalesce(subject, ''), body, human_edited, version, updated_at
		FROM campaign_contents WHERE campaign_id = $1
		ORDER BY CASE channel WHEN 'sms' THEN 0 ELSE 1 END`, id)
	if err != nil {
		return Campaign{}, fmt.Errorf("get content of campaign %d: %w", id, err)
	}
	c.Contents, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Content, error) {
		var m Content
		err := row.Scan(&m.Channel, &m.Subject, &m.Body, &m.HumanEdited, &m.Version, &m.UpdatedAt)
		return m, err
	})
	if err != nil {
		return Campaign{}, fmt.Errorf("get content of campaign %d: %w", id, err)
	}
	if c.Contents == nil {
		c.Contents = []Content{}
	}
	return c, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func (s *PGStore) SaveSegment(ctx context.Context, id int64, r segment.Rule, a segment.Audience) error {
	rule, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode segment: %w", err)
	}
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE campaigns SET segment_rule = $2, audience_count = $3, audience_sms = $4, audience_email = $5,
			       status = 'audience_ready', last_error = NULL, updated_at = now()
			WHERE id = $1 AND `+editableSQL, id, rule, a.Matched, a.SMS, a.Email)
		if err != nil {
			return fmt.Errorf("save segment: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return s.conflict(ctx, tx, id)
		}
		// Content was written for the previous audience.
		if _, err := tx.Exec(ctx, "DELETE FROM campaign_contents WHERE campaign_id = $1", id); err != nil {
			return fmt.Errorf("clear content: %w", err)
		}
		return nil
	})
}

func (s *PGStore) SaveContent(ctx context.Context, id int64, c content.Content) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE campaigns SET status = 'content_ready', last_error = NULL, updated_at = now()
			WHERE id = $1 AND status IN ('audience_ready', 'content_ready')`, id)
		if err != nil {
			return fmt.Errorf("save content: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return s.conflict(ctx, tx, id)
		}
		for _, m := range messages(c) {
			_, err := tx.Exec(ctx, `
				INSERT INTO campaign_contents (campaign_id, channel, subject, body) VALUES ($1, $2, $3, $4)
				ON CONFLICT (campaign_id, channel) DO UPDATE
				SET subject = EXCLUDED.subject, body = EXCLUDED.body, human_edited = false,
				    version = campaign_contents.version + 1, updated_at = now()`,
				id, m.channel, m.subject, m.body)
			if err != nil {
				return fmt.Errorf("save %s content: %w", m.channel, err)
			}
		}
		return nil
	})
}

func (s *PGStore) EditContent(ctx context.Context, id int64, c content.Content, channel string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// Lock the campaign row so an approval cannot slip in between the
		// status check and the content update.
		var status string
		err := tx.QueryRow(ctx, "SELECT status FROM campaigns WHERE id = $1 FOR UPDATE", id).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock campaign: %w", err)
		}
		if status != StatusContentReady {
			return fmt.Errorf("%w: content can no longer be edited (status %s)", ErrConflict, status)
		}
		for _, m := range messages(c) {
			if m.channel != channel {
				continue
			}
			tag, err := tx.Exec(ctx, `
				UPDATE campaign_contents SET subject = $3, body = $4, human_edited = true,
				       version = version + 1, updated_at = now()
				WHERE campaign_id = $1 AND channel = $2`, id, m.channel, m.subject, m.body)
			if err != nil {
				return fmt.Errorf("edit %s content: %w", channel, err)
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("%w: no %s content to edit", ErrConflict, channel)
			}
		}
		_, err = tx.Exec(ctx, "UPDATE campaigns SET updated_at = now() WHERE id = $1", id)
		return err
	})
}

func (s *PGStore) SetError(ctx context.Context, id int64, msg string) error {
	if _, err := s.db.Exec(ctx, "UPDATE campaigns SET last_error = $2, updated_at = now() WHERE id = $1", id, msg); err != nil {
		return fmt.Errorf("set campaign error: %w", err)
	}
	return nil
}

// conflict explains why a guarded update matched no rows.
func (s *PGStore) conflict(ctx context.Context, tx pgx.Tx, id int64) error {
	var status string
	err := tx.QueryRow(ctx, "SELECT status FROM campaigns WHERE id = $1", id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w (status %s)", ErrConflict, status)
}

type message struct {
	channel string
	subject *string // NULL for SMS
	body    string
}

func messages(c content.Content) []message {
	var out []message
	if c.SMS != nil {
		out = append(out, message{channel: content.ChannelSMS, body: c.SMS.Body})
	}
	if c.Email != nil {
		out = append(out, message{channel: content.ChannelEmail, subject: &c.Email.Subject, body: c.Email.Body})
	}
	return out
}

func (s *PGStore) Catalog(ctx context.Context) (segment.Catalog, error) {
	return segment.LoadCatalog(ctx, s.db)
}

func (s *PGStore) Count(ctx context.Context, r segment.Rule, now time.Time) (segment.Audience, error) {
	return segment.Count(ctx, s.db, r, now)
}

// variableColumns maps personalization variables to the user columns they
// are filled from. The worker renders from the same columns.
var variableColumns = map[string]string{
	content.VarFirstName: "u.first_name",
	content.VarCity:      "u.city",
}

// MaxLengths measures the longest value of each variable among users the rule
// matches who consented to at least one campaign channel. Measuring over both
// channels at once is slightly conservative for single-channel limits.
func (s *PGStore) MaxLengths(ctx context.Context, r segment.Rule, channels []string, now time.Time) (content.MaxLengths, error) {
	where, args, err := segment.ReachableWhere(r, channels, now)
	if err != nil {
		return nil, err
	}
	lens := content.MaxLengths{}
	for _, name := range content.Variables {
		var n int
		sql := "SELECT coalesce(max(char_length(" + variableColumns[name] + ")), 0) FROM users u WHERE " + where
		if err := s.db.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			return nil, fmt.Errorf("measure %s: %w", name, err)
		}
		lens[name] = n
	}
	return lens, nil
}

func (s *PGStore) Recorder(campaignID int64) llm.Recorder {
	return &recorder{db: s.db, campaignID: campaignID}
}

type recorder struct {
	db         *pgxpool.Pool
	campaignID int64
}

func (r *recorder) RecordAttempt(ctx context.Context, a llm.Attempt) error {
	var errText *string
	if a.Error != "" {
		errText = &a.Error
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO llm_generations (campaign_id, kind, model, prompt_version, attempt, raw_output, parsed,
		                             validation_errors, valid, error, input_tokens, output_tokens, latency_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		r.campaignID, a.Kind, a.Model, a.PromptVersion, a.Attempt, a.RawOutput, jsonb(a.Parsed),
		jsonb(a.ValidationErrors), a.Valid, errText, a.InputTokens, a.OutputTokens, a.Latency.Milliseconds())
	if err != nil {
		return fmt.Errorf("record llm attempt: %w", err)
	}
	return nil
}

// jsonb passes nil as SQL NULL rather than an empty JSON value.
func jsonb(b json.RawMessage) any {
	if b == nil {
		return nil
	}
	return string(b)
}
