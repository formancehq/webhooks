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
	)

	return migrator.Up(ctx)
}
