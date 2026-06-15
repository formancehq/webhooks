package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/uptrace/bun/dialect/pgdialect"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

type Store struct {
	db *bun.DB
}

var _ storage.Store = &Store{}

func NewStore(db *bun.DB) (storage.Store, error) {
	return Store{db: db}, nil
}

func (s Store) FindManyConfigs(ctx context.Context, filters map[string]any) ([]webhooks.Config, error) {
	res := []webhooks.Config{}
	sq := s.db.NewSelect().Model(&res)
	for key, val := range filters {
		switch key {
		case "id":
			sq = sq.Where("id = ?", val)
		case "endpoint":
			sq = sq.Where("endpoint = ?", val)
		case "active":
			sq = sq.Where("active = ?", val)
		case "event_types":
			sq = sq.Where("? = ANY (event_types)", val)
		default:
			return nil, fmt.Errorf("unsupported filter key: %s", key)
		}
	}
	sq.Order("updated_at DESC")
	if err := sq.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "selecting configs")
	}

	return res, nil
}

func (s Store) InsertOneConfig(ctx context.Context, cfgUser webhooks.ConfigUser) (webhooks.Config, error) {
	cfg := webhooks.NewConfig(cfgUser)
	if _, err := s.db.NewInsert().Model(&cfg).Exec(ctx); err != nil {
		return webhooks.Config{}, errors.Wrap(err, "insert one config")
	}

	return cfg, nil
}

func (s Store) UpdateOneConfig(ctx context.Context, id string, cfgUser webhooks.ConfigUser) error {
	if _, err := s.db.NewUpdate().
		Model(&webhooks.Config{}).
		Where("id = ?", id).
		Set("endpoint = ?", cfgUser.Endpoint).
		Set("secret = ?", cfgUser.Secret).
		Set("event_types = ?", pgdialect.Array(cfgUser.EventTypes)).
		Exec(ctx); err != nil {
		return errors.Wrap(err, "updating config")
	}

	return nil
}

func (s Store) DeleteOneConfig(ctx context.Context, id string) error {
	cfg := webhooks.Config{}
	if err := s.db.NewSelect().Model(&cfg).
		Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrConfigNotFound
		}
		return errors.Wrap(err, "selecting one config before deleting")
	}

	if _, err := s.db.NewDelete().Model((*webhooks.Config)(nil)).
		Where("id = ?", id).Exec(ctx); err != nil {
		return errors.Wrap(err, "deleting one config")
	}

	return nil
}

func (s Store) UpdateOneConfigActivation(ctx context.Context, id string, active bool) (webhooks.Config, error) {
	cfg := webhooks.Config{}
	if err := s.db.NewSelect().Model(&cfg).
		Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return webhooks.Config{}, storage.ErrConfigNotFound
		}
		return webhooks.Config{}, errors.Wrap(err, "selecting one config before updating activation")
	}
	if cfg.Active == active {
		return cfg, storage.ErrConfigNotModified
	}

	if _, err := s.db.NewUpdate().Model((*webhooks.Config)(nil)).
		Where("id = ?", id).
		Set("active = ?", active).
		Set("updated_at = ?", time.Now().UTC()).
		Exec(ctx); err != nil {
		return webhooks.Config{}, errors.Wrap(err, "updating one config activation")
	}

	cfg.Active = active
	return cfg, nil
}

func (s Store) UpdateOneConfigSecret(ctx context.Context, id, secret string) (webhooks.Config, error) {
	cfg := webhooks.Config{}
	if err := s.db.NewSelect().Model(&cfg).
		Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return webhooks.Config{}, storage.ErrConfigNotFound
		}
		return webhooks.Config{}, errors.Wrap(err, "selecting one config before updating secret")
	}
	if cfg.Secret == secret {
		return cfg, storage.ErrConfigNotModified
	}

	if _, err := s.db.NewUpdate().Model((*webhooks.Config)(nil)).
		Where("id = ?", id).
		Set("secret = ?", secret).
		Set("updated_at = ?", time.Now().UTC()).
		Exec(ctx); err != nil {
		return webhooks.Config{}, errors.Wrap(err, "updating one config secret")
	}

	cfg.Secret = secret
	return cfg, nil
}

func (s Store) FindAttemptsToRetryByWebhookID(ctx context.Context, webhookID string) ([]webhooks.Attempt, error) {
	res := []webhooks.Attempt{}
	if err := s.db.NewSelect().Model(&res).
		Where("webhook_id = ?", webhookID).
		Where("status = ?", webhooks.StatusAttemptRetrying).
		Order("created_at DESC").
		Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "finding attempts to retry")
	}

	return res, nil
}

func (s Store) FindFirstAttemptCreatedAtByWebhookID(ctx context.Context, webhookID string) (time.Time, error) {
	var att webhooks.Attempt
	if err := s.db.NewSelect().Model(&att).
		Column("created_at").
		Where("webhook_id = ?", webhookID).
		Order("created_at ASC").
		Limit(1).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, storage.ErrWebhookIDNotFound
		}
		return time.Time{}, errors.Wrap(err, "finding first attempt created_at")
	}

	return att.CreatedAt, nil
}

func (s Store) FindWebhookIDsToRetry(ctx context.Context, limit int) ([]string, error) {
	// Raw SQL is required here: the atomic claim pattern (SELECT + UPDATE in a single
	// statement via CTE) cannot be expressed with Bun's query builder.
	webhookIDs := []string{}
	_, err := s.db.NewRaw(`
		WITH to_claim AS (
			SELECT attempts.webhook_id
			FROM attempts
			JOIN configs c ON c.id = attempts.config->>'id'
			WHERE attempts.status = ?
			  AND attempts.next_retry_after < NOW()
			  AND c.active = true
			  AND NOT EXISTS (
				  SELECT 1
				  FROM attempts older
				  JOIN configs older_c ON older_c.id = older.config->>'id'
				  WHERE older.webhook_id = attempts.webhook_id
				    AND older.status = ?
				    AND older.next_retry_after < NOW()
				    AND older_c.active = true
				    AND (older.next_retry_after, older.id) < (attempts.next_retry_after, attempts.id)
			  )
			ORDER BY attempts.next_retry_after ASC, attempts.id ASC
			FOR UPDATE OF attempts SKIP LOCKED
			LIMIT ?
		),
		attempts_to_claim AS (
			SELECT attempts.id
			FROM attempts
			JOIN configs c ON c.id = attempts.config->>'id'
			WHERE attempts.webhook_id IN (SELECT webhook_id FROM to_claim)
			  AND c.active = true
			  AND attempts.status = ?
			  AND attempts.next_retry_after < NOW()
			FOR UPDATE OF attempts SKIP LOCKED
		),
		claimed AS (
			UPDATE attempts
			SET status = ?, updated_at = NOW()
			FROM attempts_to_claim
			WHERE attempts.id = attempts_to_claim.id
			RETURNING attempts.webhook_id
		)
		SELECT DISTINCT webhook_id FROM claimed
	`, webhooks.StatusAttemptToRetry, webhooks.StatusAttemptToRetry, limit,
		webhooks.StatusAttemptToRetry, webhooks.StatusAttemptRetrying,
	).Exec(ctx, &webhookIDs)
	if err != nil {
		return nil, errors.Wrap(err, "claiming webhook IDs to retry")
	}

	return webhookIDs, nil
}

func (s Store) RecoverStaleRetryingAttempts(ctx context.Context, staleDuration time.Duration) error {
	_, err := s.db.NewUpdate().
		Model((*webhooks.Attempt)(nil)).
		Where("status = ?", webhooks.StatusAttemptRetrying).
		Where("updated_at < ?", time.Now().UTC().Add(-staleDuration)).
		Set("status = ?", webhooks.StatusAttemptToRetry).
		Set("updated_at = ?", time.Now().UTC()).
		Exec(ctx)
	return errors.Wrap(err, "recovering stale retrying attempts")
}

func (s Store) UpdateAttemptsStatus(ctx context.Context, webhookID, status string) ([]webhooks.Attempt, error) {
	return updateAttemptsStatus(ctx, s.db, webhookID, status)
}

func updateAttemptsStatus(ctx context.Context, db bun.IDB, webhookID, status string) ([]webhooks.Attempt, error) {
	atts := []webhooks.Attempt{}
	if err := db.NewSelect().Model(&atts).
		Where("webhook_id = ?", webhookID).
		Where("status = ?", webhooks.StatusAttemptRetrying).
		Scan(ctx); err != nil {
		return []webhooks.Attempt{}, errors.Wrap(err, "selecting attempts by webhook ID before updating status")
	}
	if len(atts) == 0 {
		return []webhooks.Attempt{}, storage.ErrWebhookIDNotFound
	}

	if status == webhooks.StatusAttemptRetrying {
		return []webhooks.Attempt{}, storage.ErrAttemptsNotModified
	}

	if _, err := db.NewUpdate().Model((*webhooks.Attempt)(nil)).
		Where("webhook_id = ?", webhookID).
		Where("status = ?", webhooks.StatusAttemptRetrying).
		Set("status = ?", status).
		Set("updated_at = ?", time.Now().UTC()).
		Exec(ctx); err != nil {
		return []webhooks.Attempt{}, errors.Wrap(err, "updating attempts status")
	}

	for i := range atts {
		atts[i].Status = status
	}

	return atts, nil
}

func (s Store) InsertOneAttempt(ctx context.Context, att webhooks.Attempt) error {
	if _, err := s.db.NewInsert().Model(&att).Exec(ctx); err != nil {
		return errors.Wrap(err, "inserting one attempt")
	}

	return nil
}

func (s Store) InsertOneAttemptAndUpdateAttemptsStatus(ctx context.Context, att webhooks.Attempt, webhookID string, status string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "beginning attempt transaction")
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.NewInsert().Model(&att).Exec(ctx); err != nil {
		return errors.Wrap(err, "inserting one attempt")
	}
	if _, err := updateAttemptsStatus(ctx, tx, webhookID, status); err != nil {
		return errors.Wrap(err, "updating attempts status")
	}
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "committing attempt transaction")
	}

	return nil
}

func (s Store) PurgeFinishedAttempts(ctx context.Context, successOlderThan, failedOlderThan time.Duration, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}

	type purgeTarget struct {
		status    string
		retention time.Duration
	}
	targets := []purgeTarget{
		{webhooks.StatusAttemptSuccess, successOlderThan},
		{webhooks.StatusAttemptFailed, failedOlderThan},
	}

	var total int64
	for _, t := range targets {
		if t.retention <= 0 {
			continue
		}
		cutoff := time.Now().UTC().Add(-t.retention)

		// Delete at most one bounded batch per status and run so first deploys
		// against a large backlog do not monopolize the attempts table.
		res, err := s.db.NewRaw(`
			DELETE FROM attempts
			WHERE id IN (
				SELECT candidate.id FROM attempts candidate
				WHERE candidate.status = ? AND candidate.updated_at < ?
				  AND (
					? != ?
					OR NOT EXISTS (
						SELECT 1
						FROM attempts pending
						WHERE pending.webhook_id = candidate.webhook_id
						  AND pending.status IN (?, ?)
					)
				  )
				LIMIT ?
				FOR UPDATE SKIP LOCKED
			)
		`, t.status, cutoff, t.status, webhooks.StatusAttemptFailed,
			webhooks.StatusAttemptToRetry, webhooks.StatusAttemptRetrying, batchSize).Exec(ctx)
		if err != nil {
			return total, errors.Wrapf(err, "purging %q attempts", t.status)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return total, errors.Wrap(err, "reading purge rows affected")
		}
		total += affected
	}

	return total, nil
}

func (s Store) FailUnclaimableAttempts(ctx context.Context, batchSize int, retryingStaleDuration time.Duration) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	if retryingStaleDuration <= 0 {
		retryingStaleDuration = 5 * time.Minute
	}
	staleCutoff := time.Now().UTC().Add(-retryingStaleDuration)

	// Update at most one bounded batch per run. Retry rows that are actively
	// claimed stay untouched unless they are stale, matching retry recovery.
	res, err := s.db.NewRaw(`
		UPDATE attempts
		SET status = ?, updated_at = NOW()
		WHERE id IN (
			SELECT id FROM attempts
			WHERE (
				status = ?
				OR (status = ? AND updated_at < ?)
			)
			  AND NOT EXISTS (
				SELECT 1
				FROM configs c
				WHERE c.id = attempts.config->>'id'
				  AND c.active = true
			  )
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
	`, webhooks.StatusAttemptFailed, webhooks.StatusAttemptToRetry,
		webhooks.StatusAttemptRetrying, staleCutoff, batchSize).Exec(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "failing unclaimable attempts")
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, errors.Wrap(err, "reading unclaimable rows affected")
	}

	return affected, nil
}

const retryQueueDepthCountLimit = 1_000_000

func (s Store) CountAttemptsToRetry(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.NewRaw(`
		SELECT count(*)
		FROM (
			SELECT 1
			FROM attempts
			WHERE status = ?
			LIMIT ?
		) bounded_attempts
	`, webhooks.StatusAttemptToRetry, retryQueueDepthCountLimit).Scan(ctx, &count); err != nil {
		return 0, errors.Wrap(err, "counting attempts to retry")
	}
	return count, nil
}

func (s Store) Close(ctx context.Context) error {
	return s.db.Close()
}
