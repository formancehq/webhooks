package cmd

import (
	"fmt"
	"time"

	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/formancehq/webhooks/pkg/storage/postgres"
	"github.com/spf13/cobra"
)

func newBackfillDeliveriesCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "backfill-deliveries",
		Short: "Backfill deliveries from the pre-deliveries attempts table",
		RunE: func(cmd *cobra.Command, _ []string) error {
			options, err := bunconnect.ConnectionOptionsFromFlags(cmd)
			if err != nil {
				return err
			}
			db, err := bunconnect.OpenSQLDB(cmd.Context(), *options)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			if err := storage.Migrate(cmd.Context(), db); err != nil {
				return err
			}
			store, err := postgres.NewStore(db)
			if err != nil {
				return err
			}
			batchSize, _ := cmd.Flags().GetInt("batch-size")
			successSince, _ := cmd.Flags().GetDuration("success-since")
			failedSince, _ := cmd.Flags().GetDuration("failed-since")
			var total int64
			for {
				migrated, err := store.BackfillDeliveries(cmd.Context(), successSince, failedSince, batchSize)
				if err != nil {
					return err
				}
				total += migrated
				if migrated == 0 {
					break
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "backfilled %d deliveries (%d total)\n", migrated, total)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "backfill complete: %d deliveries\n", total)
			return nil
		},
	}
	bunconnect.AddFlags(command.Flags())
	command.Flags().Int("batch-size", 1000, "number of pre-deliveries webhook IDs to migrate per transaction batch")
	command.Flags().Duration("success-since", 30*24*time.Hour, "retained successful delivery history to migrate")
	command.Flags().Duration("failed-since", 90*24*time.Hour, "retained failed delivery history to migrate")
	return command
}
