package cmd

import (
	"github.com/formancehq/go-libs/v2/bun/bunmigrate"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/uptrace/bun"

	"github.com/formancehq/webhooks/cmd/flag"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/spf13/cobra"
)

func newMigrateCommand() *cobra.Command {
	return bunmigrate.NewDefaultCommand(func(cmd *cobra.Command, args []string, db *bun.DB) error {
		return storage.Migrate(cmd.Context(), db)
	})
}

func handleAutoMigrate(cmd *cobra.Command, args []string) error {
	autoMigrate, _ := cmd.Flags().GetBool(flag.AutoMigrate)
	if autoMigrate {
		logging.FromContext(cmd.Context()).Info("Automatically migrating database...")
		defer func() {
			logging.FromContext(cmd.Context()).Info("Database migrated.")
		}()
		return bunmigrate.Run(cmd, args, func(cmd *cobra.Command, args []string, db *bun.DB) error {
			return storage.Migrate(cmd.Context(), db)
		})
	}
	return nil
}
