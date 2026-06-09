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
			Name: "Add webhook delivery circuits",
			Up: func(ctx context.Context, tx bun.IDB) error {
				_, err := tx.ExecContext(ctx, `
					CREATE TABLE IF NOT EXISTS webhook_delivery_circuits (
						config_id text PRIMARY KEY REFERENCES configs(id) ON DELETE CASCADE,
						state text NOT NULL DEFAULT 'closed',
						consecutive_failures integer NOT NULL DEFAULT 0,
						opened_until timestamptz,
						probe_attempt integer NOT NULL DEFAULT 0,
						last_failure_at timestamptz,
						last_failure_status_code integer,
						last_failure_reason text,
						updated_at timestamptz NOT NULL DEFAULT now(),
						CONSTRAINT webhook_delivery_circuits_state_check
							CHECK (state IN ('closed', 'open', 'half_open'))
					)
				`)
				if err != nil {
					return errors.Wrap(err, "creating webhook delivery circuits table")
				}

				_, err = tx.ExecContext(ctx, `
					CREATE INDEX IF NOT EXISTS idx_webhook_delivery_circuits_opened_until
					ON webhook_delivery_circuits (opened_until)
					WHERE state = 'open'
				`)
				return errors.Wrap(err, "creating webhook delivery circuits opened_until index")
			},
		},
	)

	return migrator.Up(ctx)
}
