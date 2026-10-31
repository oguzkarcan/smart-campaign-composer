package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
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

// Approve enqueues messages and starts sending in one transaction, so a
// campaign is never "sending" without its messages, nor are messages queued
// for content that could still change. The campaign row is locked first,
// which serializes approval with edits and with a second approval.
func (s *PGStore) Approve(ctx context.Context, id int64, versions map[string]int, now time.Time) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var (
			status   string
			channels []string
			rule     []byte
		)
		err := tx.QueryRow(ctx, "SELECT status, channels, segment_rule FROM campaigns WHERE id = $1 FOR UPDATE", id).
			Scan(&status, &channels, &rule)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock campaign: %w", err)
		}
		if status != StatusContentReady {
			return fmt.Errorf("%w: the campaign can no longer be approved (status %s)", ErrConflict, status)
		}

		// The content must be exactly what the service validated.
		rows, err := tx.Query(ctx, "SELECT channel, version FROM campaign_contents WHERE campaign_id = $1", id)
		if err != nil {
			return fmt.Errorf("read content versions: %w", err)
		}
		current := map[string]int{}
		var ch string
		var v int
		if _, err := pgx.ForEachRow(rows, []any{&ch, &v}, func() error { current[ch] = v; return nil }); err != nil {
			return fmt.Errorf("read content versions: %w", err)
		}
		if !maps.Equal(current, versions) {
			return fmt.Errorf("%w: the content changed while approving; review it and approve again", ErrConflict)
		}

		var r segment.Rule
		if err := json.Unmarshal(rule, &r); err != nil {
			return fmt.Errorf("decode segment of campaign %d: %w", id, err)
		}
		queued := 0
		for _, channel := range channels {
			n, err := enqueue(ctx, tx, id, r, channel, now)
			if err != nil {
				return err
			}
			queued += n
		}
		if queued == 0 {
			return fmt.Errorf("%w: nobody in this audience can be reached on %s anymore; change the brief or the channels",
				ErrConflict, strings.Join(channels, " or "))
		}

		_, err = tx.Exec(ctx, `
			UPDATE campaigns SET status = 'sending', approved_at = $2, last_error = NULL, updated_at = now()
			WHERE id = $1`, id, now)
		if err != nil {
			return fmt.Errorf("approve campaign: %w", err)
		}
		return nil
	})
}

// enqueue inserts one queued message per recipient of channel: users the rule
// matches who consented to it (segment.RecipientsSQL). ON CONFLICT DO NOTHING
// makes it idempotent against UNIQUE(campaign_id, user_id, channel).
func enqueue(ctx context.Context, tx pgx.Tx, id int64, r segment.Rule, channel string, now time.Time) (int, error) {
	recipients, args, err := segment.RecipientsSQL(r, channel, now)
	if err != nil {
		return 0, err
	}
	n := len(args)
	sql := fmt.Sprintf(`
		INSERT INTO messages (campaign_id, user_id, channel)
		SELECT $%d::bigint, r.id, $%d::text FROM (%s) r
		ON CONFLICT (campaign_id, user_id, channel) DO NOTHING`, n+1, n+2, recipients)
	tag, err := tx.Exec(ctx, sql, append(args, id, channel)...)
	if err != nil {
		return 0, fmt.Errorf("enqueue %s messages: %w", channel, err)
	}
	return int(tag.RowsAffected()), nil
}

// maxFailureReasons bounds the failure list; errors are grouped by text.
const maxFailureReasons = 5

func (s *PGStore) Progress(ctx context.Context, id int64) (Progress, error) {
	p := Progress{CampaignID: id, Channels: map[string]Counts{}}
	var channels []string
	err := s.db.QueryRow(ctx, "SELECT status, channels FROM campaigns WHERE id = $1", id).Scan(&p.Status, &channels)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, fmt.Errorf("get campaign %d: %w", id, err)
	}
	for _, ch := range channels {
		p.Channels[ch] = Counts{}
	}

	rows, err := s.db.Query(ctx, "SELECT channel, status, count(*) FROM messages WHERE campaign_id = $1 GROUP BY 1, 2", id)
	if err != nil {
		return Progress{}, fmt.Errorf("count messages: %w", err)
	}
	var ch, status string
	var n int
	_, err = pgx.ForEachRow(rows, []any{&ch, &status, &n}, func() error {
		c := p.Channels[ch]
		c.Add(status, n)
		p.Channels[ch] = c
		p.Totals.Add(status, n)
		return nil
	})
	if err != nil {
		return Progress{}, fmt.Errorf("count messages: %w", err)
	}

	rows, err = s.db.Query(ctx, `
		SELECT coalesce(last_error, 'unknown error'), count(*) FROM messages
		WHERE campaign_id = $1 AND status = 'failed'
		GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT $2`, id, maxFailureReasons)
	if err != nil {
		return Progress{}, fmt.Errorf("read failures: %w", err)
	}
	p.Failures, err = pgx.CollectRows(rows, pgx.RowToStructByPos[FailureReason])
	if err != nil {
		return Progress{}, fmt.Errorf("read failures: %w", err)
	}
	return p, nil
}

func (s *PGStore) List(ctx context.Context, limit int, before int64) ([]Summary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.brief, c.status, c.channels, coalesce(c.last_error, ''), c.approved_at, c.created_at, c.updated_at,
		       coalesce(m.total, 0), coalesce(m.queued, 0), coalesce(m.processing, 0),
		       coalesce(m.retry, 0), coalesce(m.sent, 0), coalesce(m.failed, 0)
		FROM campaigns c
		LEFT JOIN LATERAL (
			SELECT count(*) AS total,
			       count(*) FILTER (WHERE status = 'queued') AS queued,
			       count(*) FILTER (WHERE status = 'processing') AS processing,
			       count(*) FILTER (WHERE status = 'retry') AS retry,
			       count(*) FILTER (WHERE status = 'sent') AS sent,
			       count(*) FILTER (WHERE status = 'failed') AS failed
			FROM messages WHERE campaign_id = c.id
		) m ON true
		WHERE $2 = 0 OR c.id < $2
		ORDER BY c.id DESC
		LIMIT $1`, limit, before)
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Summary, error) {
		var c Summary
		m := &c.Messages
		err := row.Scan(&c.ID, &c.Brief, &c.Status, &c.Channels, &c.LastError, &c.ApprovedAt, &c.CreatedAt, &c.UpdatedAt,
			&m.Total, &m.Queued, &m.Processing, &m.Retry, &m.Sent, &m.Failed)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	return list, nil
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
