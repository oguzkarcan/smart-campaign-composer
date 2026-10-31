package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGQueue is the message queue in Postgres. Times come from the database
// clock (now()), so several processes never disagree about due times or
// leases.
type PGQueue struct {
	db *pgxpool.Pool
}

func NewPGQueue(db *pgxpool.Pool) *PGQueue { return &PGQueue{db: db} }

// Claim takes up to n due messages of channel, oldest due first, and leases
// them for lease. FOR UPDATE SKIP LOCKED lets concurrent claimers take
// different rows without waiting on each other. The attempt is counted at
// claim time, so a worker that dies mid-send still uses up an attempt and a
// message cannot be retried forever. Each job carries its recipient and the
// campaign's locked content in the language recorded for the recipient at
// approval, so sending needs no further queries.
func (q *PGQueue) Claim(ctx context.Context, channel string, n int, lease time.Duration) ([]Job, error) {
	rows, err := q.db.Query(ctx, `
		WITH due AS (
			SELECT id FROM messages
			WHERE channel = $1 AND status IN ('queued', 'retry') AND next_attempt_at <= now()
			ORDER BY next_attempt_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE messages m
		SET status = 'processing', attempts = m.attempts + 1,
		    lease_until = now() + make_interval(secs => $3), updated_at = now()
		FROM due, users u, campaign_contents cc
		WHERE m.id = due.id AND u.id = m.user_id
		  AND cc.campaign_id = m.campaign_id AND cc.channel = m.channel AND cc.language = m.language
		RETURNING m.id, m.campaign_id, m.channel, m.language, m.attempts,
		          u.email, u.phone, u.first_name, u.city, coalesce(cc.subject, ''), cc.body`,
		channel, n, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim %s messages: %w", channel, err)
	}
	jobs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Job, error) {
		var j Job
		err := row.Scan(&j.MessageID, &j.CampaignID, &j.Channel, &j.Language, &j.Attempt,
			&j.Email, &j.Phone, &j.FirstName, &j.City, &j.Subject, &j.Body)
		return j, err
	})
	if err != nil {
		return nil, fmt.Errorf("claim %s messages: %w", channel, err)
	}
	return jobs, nil
}

// Finish records the provider call in message_attempts and applies the
// decision. The message is updated only if this worker still holds it
// (status processing at the same attempt); if the lease expired and the
// message was reclaimed meanwhile, the call is still recorded but the newer
// attempt owns the status, and Finish reports false. A late "sent" is then
// harmless: the next attempt uses the same idempotency key and is replayed.
func (q *PGQueue) Finish(ctx context.Context, j Job, res Result, d Decision) (bool, error) {
	var status *int
	if res.StatusCode != 0 {
		status = &res.StatusCode
	}
	var errText *string
	if res.Err != nil {
		s := res.Err.Error()
		errText = &s
	}
	var providerID *string
	if res.ProviderMessageID != "" {
		providerID = &res.ProviderMessageID
	}

	var updated bool
	err := pgx.BeginFunc(ctx, q.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO message_attempts (message_id, attempt, http_status, error, latency_ms, outcome)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (message_id, attempt) DO NOTHING`,
			j.MessageID, j.Attempt, status, errText, res.Latency.Milliseconds(), res.Outcome.String())
		if err != nil {
			return fmt.Errorf("record attempt: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE messages
			SET status = $3, last_error = $4, provider_message_id = coalesce($5, provider_message_id),
			    sent_at = CASE WHEN $3 = 'sent' THEN now() END,
			    next_attempt_at = CASE WHEN $3 = 'retry' THEN now() + make_interval(secs => $6) ELSE next_attempt_at END,
			    lease_until = NULL, updated_at = now()
			WHERE id = $1 AND status = 'processing' AND attempts = $2`,
			j.MessageID, j.Attempt, d.Status, errText, providerID, d.Delay.Seconds())
		if err != nil {
			return fmt.Errorf("update message: %w", err)
		}
		updated = tag.RowsAffected() == 1
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("finish message %d: %w", j.MessageID, err)
	}
	return updated, nil
}

// Release returns claimed jobs that were never sent (the worker is shutting
// down) to the queue, giving back the attempt the claim counted.
func (q *PGQueue) Release(ctx context.Context, jobs []Job) error {
	if len(jobs) == 0 {
		return nil
	}
	ids := make([]int64, len(jobs))
	attempts := make([]int32, len(jobs))
	for i, j := range jobs {
		ids[i], attempts[i] = j.MessageID, int32(j.Attempt)
	}
	_, err := q.db.Exec(ctx, `
		UPDATE messages m
		SET status = CASE WHEN m.attempts > 1 THEN 'retry' ELSE 'queued' END,
		    attempts = m.attempts - 1, lease_until = NULL, next_attempt_at = now(), updated_at = now()
		FROM unnest($1::bigint[], $2::int[]) AS r(id, attempt)
		WHERE m.id = r.id AND m.status = 'processing' AND m.attempts = r.attempt`, ids, attempts)
	if err != nil {
		return fmt.Errorf("release messages: %w", err)
	}
	return nil
}

// RecoverLeases puts messages whose lease expired (their worker died or hung)
// back in the queue, or fails them if they used up their attempts. No
// message_attempts row is written: if the original call does return, Finish
// records it under its own attempt number.
func (q *PGQueue) RecoverLeases(ctx context.Context) (int, error) {
	tag, err := q.db.Exec(ctx, `
		UPDATE messages
		SET status = CASE WHEN attempts >= $1 THEN 'failed' ELSE 'retry' END,
		    last_error = 'lease expired: the attempt did not finish in time',
		    lease_until = NULL, next_attempt_at = now(), updated_at = now()
		WHERE status = 'processing' AND lease_until < now()`, MaxAttempts)
	if err != nil {
		return 0, fmt.Errorf("recover leases: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// StartScheduled moves scheduled campaigns whose time has come to sending
// and returns their ids. Their messages were queued at approval, due at
// scheduled_at, so the channel loops pick them up on their own; the status
// change lets FinishCampaigns close the campaign once they are done, and
// never earlier.
func (q *PGQueue) StartScheduled(ctx context.Context) ([]int64, error) {
	rows, err := q.db.Query(ctx, `
		UPDATE campaigns SET status = 'sending', updated_at = now()
		WHERE status = 'scheduled' AND scheduled_at <= now()
		RETURNING id`)
	if err != nil {
		return nil, fmt.Errorf("start scheduled campaigns: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("start scheduled campaigns: %w", err)
	}
	return ids, nil
}

// CampaignDone is a campaign that finished sending.
type CampaignDone struct {
	ID     int64
	Status string
}

// FinishCampaigns closes sending campaigns that have no message left to
// send: "completed" if at least one message was sent, "failed" if none was.
func (q *PGQueue) FinishCampaigns(ctx context.Context) ([]CampaignDone, error) {
	rows, err := q.db.Query(ctx, `
		UPDATE campaigns c
		SET status = CASE WHEN EXISTS (SELECT 1 FROM messages m WHERE m.campaign_id = c.id AND m.status = 'sent')
		                  THEN 'completed' ELSE 'failed' END,
		    updated_at = now()
		WHERE c.status = 'sending'
		  AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.campaign_id = c.id
		                  AND m.status IN ('queued', 'retry', 'processing'))
		RETURNING c.id, c.status`)
	if err != nil {
		return nil, fmt.Errorf("finish campaigns: %w", err)
	}
	done, err := pgx.CollectRows(rows, pgx.RowToStructByPos[CampaignDone])
	if err != nil {
		return nil, fmt.Errorf("finish campaigns: %w", err)
	}
	return done, nil
}
