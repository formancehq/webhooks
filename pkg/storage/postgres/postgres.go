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

func (s Store) BeforeDelivery(ctx context.Context, configID string) (webhooks.CircuitDecision, error) {
	now := time.Now().UTC()

	circuit, err := s.findDeliveryCircuit(ctx, s.db, configID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return webhooks.CircuitDecision{}, errors.Wrap(err, "finding delivery circuit")
		}

		if err := ensureDeliveryCircuit(ctx, s.db, configID); err != nil {
			return webhooks.CircuitDecision{}, errors.Wrap(err, "ensuring delivery circuit")
		}
		return webhooks.CircuitDecision{
			Allowed: true,
			Reason:  webhooks.CircuitDecisionReasonClosed,
		}, nil
	}

	switch circuit.State {
	case webhooks.CircuitStateClosed:
		return webhooks.CircuitDecision{
			Allowed: true,
			Reason:  webhooks.CircuitDecisionReasonClosed,
		}, nil
	case webhooks.CircuitStateHalfOpen:
		return webhooks.CircuitDecision{
			Allowed: false,
			Reason:  webhooks.CircuitDecisionReasonProbeBusy,
		}, nil
	case webhooks.CircuitStateOpen:
		if !circuit.OpenedUntil.IsZero() && !circuit.OpenedUntil.After(now) {
			claimed, err := s.claimHalfOpenProbe(ctx, configID, now)
			if err != nil {
				return webhooks.CircuitDecision{}, errors.Wrap(err, "claiming half-open probe")
			}
			if claimed {
				return webhooks.CircuitDecision{
					Allowed: true,
					Reason:  webhooks.CircuitDecisionReasonHalfOpenProbe,
				}, nil
			}

			return webhooks.CircuitDecision{
				Allowed: false,
				Reason:  webhooks.CircuitDecisionReasonProbeBusy,
			}, nil
		}

		return webhooks.CircuitDecision{
			Allowed:     false,
			Reason:      webhooks.CircuitDecisionReasonOpenUntil,
			OpenedUntil: circuit.OpenedUntil,
		}, nil
	default:
		return webhooks.CircuitDecision{}, fmt.Errorf("unknown delivery circuit state %q", circuit.State)
	}
}

func (s Store) RecordSuccess(ctx context.Context, configID string) error {
	if err := ensureDeliveryCircuit(ctx, s.db, configID); err != nil {
		return errors.Wrap(err, "ensuring delivery circuit")
	}

	_, err := s.db.NewUpdate().
		Model((*webhooks.DeliveryCircuit)(nil)).
		Where("config_id = ?", configID).
		Set("state = ?", webhooks.CircuitStateClosed).
		Set("consecutive_failures = 0").
		Set("opened_until = NULL").
		Set("probe_attempt = 0").
		Set("last_failure_at = NULL").
		Set("last_failure_status_code = NULL").
		Set("last_failure_reason = NULL").
		Set("updated_at = ?", time.Now().UTC()).
		Exec(ctx)
	return errors.Wrap(err, "recording delivery circuit success")
}

func (s Store) RecordFailure(ctx context.Context, configID string, failure webhooks.DeliveryFailure) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := ensureDeliveryCircuit(ctx, tx, configID); err != nil {
			return errors.Wrap(err, "ensuring delivery circuit")
		}

		circuit, err := s.findDeliveryCircuit(ctx, tx, configID)
		if err != nil {
			return errors.Wrap(err, "finding delivery circuit")
		}

		now := time.Now().UTC()
		circuit.ConsecutiveFailures++
		circuit.LastFailureAt = now
		circuit.LastFailureStatusCode = failure.StatusCode
		circuit.LastFailureReason = deliveryFailureReason(failure)
		circuit.UpdatedAt = now

		switch circuit.State {
		case webhooks.CircuitStateHalfOpen:
			circuit.State = webhooks.CircuitStateOpen
			circuit.ProbeAttempt++
			circuit.OpenedUntil = now.Add(webhooks.CircuitProbeDelay(circuit.ProbeAttempt))
		case webhooks.CircuitStateClosed:
			if circuit.ConsecutiveFailures >= webhooks.DefaultCircuitFailureThreshold {
				circuit.State = webhooks.CircuitStateOpen
				circuit.ProbeAttempt = 0
				circuit.OpenedUntil = now.Add(webhooks.DefaultCircuitOpenDelay)
			}
		case webhooks.CircuitStateOpen:
			// A race can record a failure after another worker already opened the
			// circuit. Keep the current open window and only refresh failure details.
		default:
			return fmt.Errorf("unknown delivery circuit state %q", circuit.State)
		}

		_, err = tx.NewUpdate().
			Model(&circuit).
			WherePK().
			Column("state").
			Column("consecutive_failures").
			Column("opened_until").
			Column("probe_attempt").
			Column("last_failure_at").
			Column("last_failure_status_code").
			Column("last_failure_reason").
			Column("updated_at").
			Exec(ctx)
		return errors.Wrap(err, "recording delivery circuit failure")
	})
}

func (s Store) findDeliveryCircuit(ctx context.Context, db bun.IDB, configID string) (webhooks.DeliveryCircuit, error) {
	circuit := webhooks.DeliveryCircuit{}
	if err := db.NewSelect().
		Model(&circuit).
		Where("config_id = ?", configID).
		For("UPDATE").
		Scan(ctx); err != nil {
		return webhooks.DeliveryCircuit{}, err
	}
	return circuit, nil
}

func (s Store) claimHalfOpenProbe(ctx context.Context, configID string, now time.Time) (bool, error) {
	circuit := webhooks.DeliveryCircuit{}
	err := s.db.NewRaw(`
		UPDATE webhook_delivery_circuits
		SET state = ?, updated_at = ?
		WHERE config_id = ?
		  AND state = ?
		  AND opened_until <= ?
		RETURNING *
	`, webhooks.CircuitStateHalfOpen, now, configID, webhooks.CircuitStateOpen, now).Scan(ctx, &circuit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func ensureDeliveryCircuit(ctx context.Context, db bun.IDB, configID string) error {
	circuit := webhooks.NewDeliveryCircuit(configID)
	_, err := db.NewInsert().
		Model(&circuit).
		On("CONFLICT (config_id) DO NOTHING").
		Exec(ctx)
	return err
}

func deliveryFailureReason(failure webhooks.DeliveryFailure) string {
	if failure.Reason != "" {
		return failure.Reason
	}
	if failure.StatusCode == 0 {
		return "transport_error"
	}
	return fmt.Sprintf("status_code_%d", failure.StatusCode)
}

func (s Store) Close(ctx context.Context) error {
	return s.db.Close()
}
