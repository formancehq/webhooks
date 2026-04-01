package postgres

import (
	"context"
	"database/sql"
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
			panic(key)
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

func (s Store) FindWebhookIDsToRetry(ctx context.Context, limit int) ([]string, error) {
	// Raw SQL is required here: the atomic claim pattern (SELECT + UPDATE in a single
	// statement via CTE) cannot be expressed with Bun's query builder.
	webhookIDs := []string{}
	_, err := s.db.NewRaw(`
		WITH to_claim AS (
			SELECT DISTINCT ON (webhook_id) webhook_id
			FROM attempts
			JOIN configs c ON c.id = attempts.config->>'id'
			WHERE attempts.status = ?
			  AND attempts.next_retry_after < NOW()
			ORDER BY webhook_id, attempts.next_retry_after ASC
			LIMIT ?
		),
		claimed AS (
			UPDATE attempts
			SET status = ?, updated_at = NOW()
			WHERE webhook_id IN (SELECT webhook_id FROM to_claim)
			  AND status = ?
			RETURNING webhook_id
		)
		SELECT DISTINCT webhook_id FROM claimed
	`, webhooks.StatusAttemptToRetry, limit,
		webhooks.StatusAttemptRetrying, webhooks.StatusAttemptToRetry,
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
	atts := []webhooks.Attempt{}
	if err := s.db.NewSelect().Model(&atts).
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

	if _, err := s.db.NewUpdate().Model((*webhooks.Attempt)(nil)).
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

func (s Store) Close(ctx context.Context) error {
	return s.db.Close()
}
