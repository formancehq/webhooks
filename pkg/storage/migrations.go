package storage

import (
	"context"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/pkg/errors"

	"github.com/formancehq/go-libs/v2/migrations"
	"github.com/uptrace/bun"
)

func Migrate(ctx context.Context, db *bun.DB) error {
	migrator := migrations.NewMigrator(db)
	migrator.RegisterMigrations(
		migrations.Migration{
			Name: "Init schema",
			Up: func(ctx context.Context, tx bun.IDB) error {
				_, err := tx.NewCreateTable().Model((*webhooks.Config)(nil)).
					IfNotExists().
					Exec(ctx)
				if err != nil {
					return errors.Wrap(err, "creating 'configs' table")
				}
				_, err = tx.NewCreateIndex().Model((*webhooks.Config)(nil)).
					IfNotExists().
					Index("configs_idx").
					Column("event_types").
					Exec(ctx)
				if err != nil {
					return errors.Wrap(err, "creating index on 'configs' table")
				}
				_, err = tx.NewCreateTable().Model((*webhooks.Attempt)(nil)).
					IfNotExists().
					Exec(ctx)
				if err != nil {
					return errors.Wrap(err, "creating 'attempts' table")
				}
				_, err = tx.NewCreateIndex().Model((*webhooks.Attempt)(nil)).
					IfNotExists().
					Index("attempts_idx").
					Column("webhook_id", "status").
					Exec(ctx)
				if err != nil {
					return errors.Wrap(err, "creating index on 'attempts' table")
				}
				return nil
			},
		},
		migrations.Migration{
			Up: func(ctx context.Context, tx bun.IDB) error {
				_, err := tx.NewAddColumn().
					Table("configs").
					ColumnExpr("name varchar(255)").
					IfNotExists().
					Exec(ctx)
				return errors.Wrap(err, "adding 'name' column")
			},
		},
		migrations.Migration{
			Name: "Add partial index for retry polling",
			Up: func(ctx context.Context, tx bun.IDB) error {
				_, err := tx.ExecContext(ctx, `
					CREATE INDEX IF NOT EXISTS idx_attempts_retry_pending
					ON attempts (next_retry_after)
					WHERE status = 'to retry'
				`)
				if err != nil {
					return errors.Wrap(err, "creating partial index for retry polling")
				}

				_, err = tx.ExecContext(ctx, `
					CREATE INDEX IF NOT EXISTS idx_attempts_retrying
					ON attempts (webhook_id)
					WHERE status = 'retrying'
				`)
				if err != nil {
					return errors.Wrap(err, "creating partial index for retrying status")
				}

				_, err = tx.ExecContext(ctx, `
					CREATE INDEX IF NOT EXISTS idx_attempts_retrying_recovery
					ON attempts (updated_at)
					WHERE status = 'retrying'
					`)
				return errors.Wrap(err, "creating partial index for retrying recovery")
			},
		},
		migrations.Migration{
			Name: "Add composite partial indexes for retry claims",
			Up: func(ctx context.Context, tx bun.IDB) error {
				_, err := tx.ExecContext(ctx, `
						DROP INDEX CONCURRENTLY IF EXISTS idx_attempts_retry_pending_due
					`)
				if err != nil {
					return errors.Wrap(err, "dropping partial index for due retry claims before rebuild")
				}

				if _, err = tx.ExecContext(ctx, `
						CREATE INDEX CONCURRENTLY idx_attempts_retry_pending_due
						ON attempts (next_retry_after, id, webhook_id)
						WHERE status = 'to retry'
					`); err != nil {
					return errors.Wrap(err, "creating partial index for due retry claims")
				}

				if _, err = tx.ExecContext(ctx, `
						DROP INDEX CONCURRENTLY IF EXISTS idx_attempts_retry_pending_webhook_due
					`); err != nil {
					return errors.Wrap(err, "dropping partial index for webhook retry claims before rebuild")
				}

				if _, err = tx.ExecContext(ctx, `
						CREATE INDEX CONCURRENTLY idx_attempts_retry_pending_webhook_due
						ON attempts (webhook_id, next_retry_after, id)
						WHERE status = 'to retry'
					`); err != nil {
					return errors.Wrap(err, "creating partial index for webhook retry claims")
				}

				_, err = tx.ExecContext(ctx, `
						DROP INDEX CONCURRENTLY IF EXISTS idx_attempts_retry_pending
					`)
				return errors.Wrap(err, "dropping redundant partial index for retry polling")
			},
		},
		migrations.Migration{
			Name: "Add partial indexes for attempts retention",
			Up: func(ctx context.Context, tx bun.IDB) error {
				if _, err := tx.ExecContext(ctx, `
							DROP INDEX CONCURRENTLY IF EXISTS idx_attempts_retention_success
						`); err != nil {
					return errors.Wrap(err, "dropping partial index for success attempts retention before rebuild")
				}

				if _, err := tx.ExecContext(ctx, `
							CREATE INDEX CONCURRENTLY idx_attempts_retention_success
							ON attempts (updated_at, id)
							WHERE status = 'success'
						`); err != nil {
					return errors.Wrap(err, "creating partial index for success attempts retention")
				}

				if _, err := tx.ExecContext(ctx, `
							DROP INDEX CONCURRENTLY IF EXISTS idx_attempts_retention_failed
						`); err != nil {
					return errors.Wrap(err, "dropping partial index for failed attempts retention before rebuild")
				}

				if _, err := tx.ExecContext(ctx, `
							CREATE INDEX CONCURRENTLY idx_attempts_retention_failed
							ON attempts (updated_at, id)
							WHERE status = 'failed'
						`); err != nil {
					return errors.Wrap(err, "creating partial index for failed attempts retention")
				}

				if _, err := tx.ExecContext(ctx, `
							DROP INDEX CONCURRENTLY IF EXISTS idx_attempts_first_attempt_lookup
						`); err != nil {
					return errors.Wrap(err, "dropping index for first attempt lookup before rebuild")
				}

				_, err := tx.ExecContext(ctx, `
							CREATE INDEX CONCURRENTLY idx_attempts_first_attempt_lookup
							ON attempts (webhook_id, created_at)
						`)
				return errors.Wrap(err, "creating index for first attempt lookup")
			},
		},
		migrations.Migration{
			Name: "Add durable deliveries pipeline",
			Up: func(ctx context.Context, tx bun.IDB) error {
				if _, err := tx.NewAddColumn().
					Table("configs").
					ColumnExpr("deleted_at timestamptz").
					IfNotExists().
					Exec(ctx); err != nil {
					return errors.Wrap(err, "adding configs.deleted_at")
				}

				if _, err := tx.NewCreateTable().Model((*webhooks.Delivery)(nil)).
					IfNotExists().Exec(ctx); err != nil {
					return errors.Wrap(err, "creating deliveries table")
				}
				if _, err := tx.NewCreateTable().Model((*webhooks.DeliveryAttempt)(nil)).
					IfNotExists().Exec(ctx); err != nil {
					return errors.Wrap(err, "creating delivery_attempts table")
				}
				if _, err := tx.NewCreateTable().Model((*webhooks.ReplayRequestRecord)(nil)).
					IfNotExists().Exec(ctx); err != nil {
					return errors.Wrap(err, "creating replay_requests table")
				}

				_, err := tx.ExecContext(ctx, `
					DO $$ BEGIN
						ALTER TABLE deliveries
							ADD CONSTRAINT deliveries_config_id_fkey
							FOREIGN KEY (config_id) REFERENCES configs(id) ON DELETE RESTRICT;
					EXCEPTION WHEN duplicate_object THEN NULL;
					END $$;
					DO $$ BEGIN
						ALTER TABLE delivery_attempts
							ADD CONSTRAINT delivery_attempts_delivery_id_fkey
							FOREIGN KEY (delivery_id) REFERENCES deliveries(id) ON DELETE CASCADE;
					EXCEPTION WHEN duplicate_object THEN NULL;
					END $$;
					CREATE UNIQUE INDEX IF NOT EXISTS idx_deliveries_event_config
						ON deliveries (event_id, config_id);
					CREATE INDEX IF NOT EXISTS idx_deliveries_pending_due
						ON deliveries (next_attempt_at, id) WHERE status = 'pending';
					CREATE INDEX IF NOT EXISTS idx_deliveries_delivering_recovery
						ON deliveries (claimed_at, id) WHERE status = 'delivering';
					CREATE INDEX IF NOT EXISTS idx_deliveries_created
						ON deliveries (created_at, id);
					CREATE INDEX IF NOT EXISTS idx_deliveries_config_status_created
						ON deliveries (config_id, status, created_at, id);
					CREATE UNIQUE INDEX IF NOT EXISTS idx_delivery_attempts_sequence
						ON delivery_attempts (delivery_id, replay_generation, attempt_number);
					CREATE INDEX IF NOT EXISTS idx_delivery_attempts_delivery_created
						ON delivery_attempts (delivery_id, created_at, id);
					CREATE INDEX IF NOT EXISTS idx_replay_requests_created
						ON replay_requests (created_at);
				`)
				return errors.Wrap(err, "creating durable delivery constraints and indexes")
			},
		},
	)

	return migrator.Up(ctx)
}
