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
	sq := s.db.NewSelect().Model(&res).Where("deleted_at IS NULL")
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
		Where("deleted_at IS NULL").
		Set("endpoint = ?", cfgUser.Endpoint).
		Set("secret = ?", cfgUser.Secret).
		Set("event_types = ?", pgdialect.Array(cfgUser.EventTypes)).
		Exec(ctx); err != nil {
		return errors.Wrap(err, "updating config")
	}

	return nil
}

func (s Store) DeleteOneConfig(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "beginning config deletion")
	}
	defer func() { _ = tx.Rollback() }()
	cfg := webhooks.Config{}
	if err := tx.NewSelect().Model(&cfg).
		Where("id = ?", id).Where("deleted_at IS NULL").For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrConfigNotFound
		}
		return errors.Wrap(err, "selecting one config before deleting")
	}
	now := time.Now().UTC()
	if _, err := tx.NewUpdate().Model((*webhooks.Config)(nil)).
		Where("id = ?", id).
		Set("active = false, deleted_at = ?, updated_at = ?", now, now).Exec(ctx); err != nil {
		return errors.Wrap(err, "soft deleting config")
	}
	if err := cancelPendingDeliveries(ctx, tx, id, now); err != nil {
		return errors.Wrap(err, "cancelling deleted config deliveries")
	}
	return errors.Wrap(tx.Commit(), "committing config deletion")
}

func (s Store) UpdateOneConfigActivation(ctx context.Context, id string, active bool) (webhooks.Config, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return webhooks.Config{}, errors.Wrap(err, "beginning config activation transaction")
	}
	defer func() { _ = tx.Rollback() }()
	cfg := webhooks.Config{}
	if err := tx.NewSelect().Model(&cfg).
		Where("id = ?", id).Where("deleted_at IS NULL").For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return webhooks.Config{}, storage.ErrConfigNotFound
		}
		return webhooks.Config{}, errors.Wrap(err, "selecting one config before updating activation")
	}
	if cfg.Active == active {
		return cfg, storage.ErrConfigNotModified
	}

	now := time.Now().UTC()
	if _, err := tx.NewUpdate().Model((*webhooks.Config)(nil)).
		Where("id = ?", id).
		Where("deleted_at IS NULL").
		Set("active = ?", active).
		Set("updated_at = ?", now).
		Exec(ctx); err != nil {
		return webhooks.Config{}, errors.Wrap(err, "updating one config activation")
	}
	if !active {
		if err := cancelPendingDeliveries(ctx, tx, id, now); err != nil {
			return webhooks.Config{}, errors.Wrap(err, "cancelling deactivated config deliveries")
		}
	}
	if err := tx.Commit(); err != nil {
		return webhooks.Config{}, errors.Wrap(err, "committing config activation")
	}

	cfg.Active = active
	cfg.UpdatedAt = now
	return cfg, nil
}

func cancelPendingDeliveries(ctx context.Context, db bun.IDB, configID string, now time.Time) error {
	_, err := db.NewUpdate().Model((*webhooks.Delivery)(nil)).
		Where("config_id = ?", configID).
		Where("status = ?", webhooks.StatusDeliveryPending).
		Set("status = ?, next_attempt_at = NULL, updated_at = ?", webhooks.StatusDeliveryCancelled, now).
		Exec(ctx)
	return err
}

func (s Store) UpdateOneConfigSecret(ctx context.Context, id, secret string) (webhooks.Config, error) {
	cfg := webhooks.Config{}
	if err := s.db.NewSelect().Model(&cfg).
		Where("id = ?", id).Where("deleted_at IS NULL").Scan(ctx); err != nil {
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
		Where("deleted_at IS NULL").
		Set("secret = ?", secret).
		Set("updated_at = ?", time.Now().UTC()).
		Exec(ctx); err != nil {
		return webhooks.Config{}, errors.Wrap(err, "updating one config secret")
	}

	cfg.Secret = secret
	return cfg, nil
}

func (s Store) Close(ctx context.Context) error {
	return s.db.Close()
}
