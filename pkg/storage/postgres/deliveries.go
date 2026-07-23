package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/formancehq/go-libs/v2/publish"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

const maxReplayPageSize = 1000

func (s Store) EnqueueEvent(ctx context.Context, eventID, idempotencyKey, eventType, payload string, createdAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "beginning event enqueue transaction")
	}
	defer func() { _ = tx.Rollback() }()
	configs := []webhooks.Config{}
	if err := tx.NewSelect().Model(&configs).
		Where("? = ANY (event_types)", eventType).
		Where("active = true AND deleted_at IS NULL").
		For("SHARE").Scan(ctx); err != nil {
		return errors.Wrap(err, "selecting configs for event enqueue")
	}
	deliveries := make([]webhooks.Delivery, 0, len(configs))
	for _, config := range configs {
		nextAttemptAt := createdAt
		deliveries = append(deliveries, webhooks.Delivery{
			ID: uuid.NewString(), EventID: eventID, IdempotencyKey: idempotencyKey,
			ConfigID: config.ID, EventType: eventType, Payload: payload,
			Status: webhooks.StatusDeliveryPending, NextAttemptAt: &nextAttemptAt,
			CreatedAt: createdAt, UpdatedAt: createdAt,
		})
	}
	if len(deliveries) > 0 {
		if _, err := tx.NewInsert().Model(&deliveries).
			On("CONFLICT (event_id, config_id) DO NOTHING").Exec(ctx); err != nil {
			return errors.Wrap(err, "inserting event deliveries")
		}
	}
	return errors.Wrap(tx.Commit(), "committing event enqueue")
}

func (s Store) ClaimDeliveries(ctx context.Context, limit int) ([]webhooks.Delivery, error) {
	if limit <= 0 {
		limit = 50
	}
	res := []webhooks.Delivery{}
	err := s.db.NewRaw(`
		WITH candidates AS (
			SELECT d.id
			FROM deliveries d
			JOIN configs c ON c.id = d.config_id
			WHERE d.status = ?
			  AND d.next_attempt_at <= NOW()
			  AND c.active = true
			  AND c.deleted_at IS NULL
			ORDER BY d.next_attempt_at, d.id
			FOR UPDATE OF d SKIP LOCKED
			LIMIT ?
		)
		UPDATE deliveries d
		SET status = ?,
			claimed_at = NOW(),
			cycle_started_at = COALESCE(cycle_started_at, NOW()),
			updated_at = NOW()
		FROM candidates
		WHERE d.id = candidates.id
		RETURNING d.*
	`, webhooks.StatusDeliveryPending, limit, webhooks.StatusDeliveryDelivering).Scan(ctx, &res)
	return res, errors.Wrap(err, "claiming deliveries")
}

func (s Store) CompleteDelivery(ctx context.Context, delivery webhooks.Delivery, attempt webhooks.DeliveryAttempt) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", errors.Wrap(err, "beginning delivery completion transaction")
	}
	defer func() { _ = tx.Rollback() }()
	config := webhooks.Config{}
	if err := tx.NewSelect().Model(&config).Where("id = ?", delivery.ConfigID).For("UPDATE").Scan(ctx); err != nil {
		return "", errors.Wrap(err, "checking delivery config before completion")
	}
	if delivery.Status != webhooks.StatusDeliverySucceeded && (!config.Active || config.DeletedAt != nil) {
		delivery.Status = webhooks.StatusDeliveryCancelled
		delivery.NextAttemptAt = nil
	}

	if _, err := tx.NewInsert().Model(&attempt).Exec(ctx); err != nil {
		return "", errors.Wrap(err, "inserting delivery attempt")
	}
	res, err := tx.NewUpdate().Model((*webhooks.Delivery)(nil)).
		Where("id = ?", delivery.ID).
		Where("status = ?", webhooks.StatusDeliveryDelivering).
		Where("claimed_at = ?", delivery.ClaimedAt).
		Set("status = ?", delivery.Status).
		Set("attempt_count = ?", delivery.AttemptCount).
		Set("cycle_started_at = ?", delivery.CycleStartedAt).
		Set("next_attempt_at = ?", delivery.NextAttemptAt).
		Set("claimed_at = NULL").
		Set("last_attempt_at = ?", delivery.LastAttemptAt).
		Set("last_status_code = ?", delivery.LastStatusCode).
		Set("last_error = ?", delivery.LastError).
		Set("updated_at = NOW()").
		Exec(ctx)
	if err != nil {
		return "", errors.Wrap(err, "updating completed delivery")
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", errors.Wrap(err, "reading completed delivery rows affected")
	}
	if affected != 1 {
		return "", storage.ErrDeliveryNotFound
	}
	if err := tx.Commit(); err != nil {
		return "", errors.Wrap(err, "committing delivery completion")
	}
	return delivery.Status, nil
}

func (s Store) CancelDelivery(ctx context.Context, id string) error {
	res, err := s.db.NewUpdate().Model((*webhooks.Delivery)(nil)).
		Where("id = ?", id).
		Where("status IN (?)", bun.List([]string{webhooks.StatusDeliveryPending, webhooks.StatusDeliveryDelivering})).
		Set("status = ?", webhooks.StatusDeliveryCancelled).
		Set("claimed_at = NULL, next_attempt_at = NULL, updated_at = NOW()").
		Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "cancelling delivery")
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return storage.ErrDeliveryNotFound
	}
	return nil
}

func (s Store) FailClaimedDelivery(ctx context.Context, id string, claimedAt time.Time, reason string) error {
	res, err := s.db.NewUpdate().Model((*webhooks.Delivery)(nil)).
		Where("id = ?", id).
		Where("status = ?", webhooks.StatusDeliveryDelivering).
		Where("claimed_at = ?", claimedAt).
		Set("status = ?", webhooks.StatusDeliveryFailed).
		Set("claimed_at = NULL, next_attempt_at = NULL, last_error = ?, updated_at = NOW()", reason).
		Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "failing claimed delivery")
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "reading failed delivery rows affected")
	}
	if affected != 1 {
		return storage.ErrDeliveryNotFound
	}
	return nil
}

func (s Store) RecoverStaleDeliveries(ctx context.Context, staleDuration time.Duration) (int64, error) {
	if staleDuration <= 0 {
		staleDuration = 5 * time.Minute
	}
	result, err := s.db.NewRaw(`
		WITH stale AS (
			SELECT d.id, c.active AND c.deleted_at IS NULL AS config_active
			FROM deliveries d
			JOIN configs c ON c.id = d.config_id
			WHERE d.status = ?
			  AND d.claimed_at < ?
			ORDER BY d.claimed_at, d.id
			FOR UPDATE OF d, c SKIP LOCKED
		)
		UPDATE deliveries d
		SET status = CASE WHEN stale.config_active THEN ? ELSE ? END,
			claimed_at = NULL,
			next_attempt_at = CASE WHEN stale.config_active THEN NOW() ELSE NULL END,
			updated_at = NOW()
		FROM stale
		WHERE d.id = stale.id
	`, webhooks.StatusDeliveryDelivering, time.Now().UTC().Add(-staleDuration),
		webhooks.StatusDeliveryPending, webhooks.StatusDeliveryCancelled).Exec(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "recovering stale deliveries")
	}
	count, err := result.RowsAffected()
	return count, errors.Wrap(err, "reading recovered delivery count")
}

func (s Store) CountPendingDeliveries(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.NewRaw(`
		SELECT COUNT(*) FROM (
			SELECT 1 FROM deliveries WHERE status = ? LIMIT 1000000
		) pending
	`, webhooks.StatusDeliveryPending).Scan(ctx, &count)
	return count, errors.Wrap(err, "counting pending deliveries")
}

func (s Store) FindDeliveries(ctx context.Context, filter webhooks.DeliveryFilter) (webhooks.DeliveryPage, error) {
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > maxReplayPageSize {
		pageSize = maxReplayPageSize
	}
	res := []webhooks.Delivery{}
	q := s.db.NewSelect().Model(&res).OrderExpr("created_at DESC, id DESC").Limit(pageSize + 1)
	if filter.ConfigID != "" {
		q = q.Where("config_id = ?", filter.ConfigID)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if !filter.CreatedAfter.IsZero() {
		q = q.Where("created_at >= ?", filter.CreatedAfter)
	}
	if !filter.CreatedBefore.IsZero() {
		q = q.Where("created_at <= ?", filter.CreatedBefore)
	}
	if filter.After != nil {
		q = q.Where("(created_at, id) < (?, ?)", filter.After.CreatedAt, filter.After.ID)
	}
	if err := q.Scan(ctx); err != nil {
		return webhooks.DeliveryPage{}, errors.Wrap(err, "finding deliveries")
	}
	page := webhooks.DeliveryPage{Data: res}
	if len(res) > pageSize {
		page.HasMore = true
		page.Data = res[:pageSize]
		last := page.Data[len(page.Data)-1]
		page.NextCursor = &webhooks.DeliveryCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (s Store) GetDelivery(ctx context.Context, id string) (webhooks.Delivery, error) {
	res := webhooks.Delivery{}
	if err := s.db.NewSelect().Model(&res).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return webhooks.Delivery{}, storage.ErrDeliveryNotFound
		}
		return webhooks.Delivery{}, errors.Wrap(err, "getting delivery")
	}
	return res, nil
}

func (s Store) FindDeliveryAttempts(ctx context.Context, deliveryID string, after *webhooks.DeliveryCursor, pageSize int) ([]webhooks.DeliveryAttempt, *webhooks.DeliveryCursor, error) {
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > maxReplayPageSize {
		pageSize = maxReplayPageSize
	}
	res := []webhooks.DeliveryAttempt{}
	q := s.db.NewSelect().Model(&res).
		Where("delivery_id = ?", deliveryID).
		OrderExpr("created_at DESC, id DESC").Limit(pageSize + 1)
	if after != nil {
		q = q.Where("(created_at, id) < (?, ?)", after.CreatedAt, after.ID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "finding delivery attempts")
	}
	var next *webhooks.DeliveryCursor
	if len(res) > pageSize {
		res = res[:pageSize]
		last := res[len(res)-1]
		next = &webhooks.DeliveryCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return res, next, nil
}

func requestHash(route string, request any) (string, error) {
	body, err := json.Marshal(struct {
		Route   string `json:"route"`
		Request any    `json:"request"`
	}{route, request})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func replayCached[T any](ctx context.Context, tx bun.Tx, key, hash string) (*T, error) {
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", key); err != nil {
		return nil, errors.Wrap(err, "locking replay idempotency key")
	}
	record := webhooks.ReplayRequestRecord{}
	err := tx.NewSelect().Model(&record).Where("key = ?", key).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "reading replay request")
	}
	if record.CreatedAt.Before(time.Now().UTC().Add(-24 * time.Hour)) {
		if _, err := tx.NewDelete().Model(&record).WherePK().Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "expiring replay request")
		}
		return nil, nil
	}
	if record.RequestHash != hash {
		return nil, storage.ErrIdempotencyConflict
	}
	var response T
	if err := json.Unmarshal([]byte(record.Response), &response); err != nil {
		return nil, errors.Wrap(err, "decoding replay response")
	}
	return &response, nil
}

func storeReplayResponse(ctx context.Context, tx bun.Tx, key, hash string, response any) error {
	body, err := json.Marshal(response)
	if err != nil {
		return errors.Wrap(err, "encoding replay response")
	}
	record := webhooks.ReplayRequestRecord{Key: key, RequestHash: hash, Response: string(body)}
	_, err = tx.NewInsert().Model(&record).Exec(ctx)
	return errors.Wrap(err, "storing replay response")
}

func (s Store) ReplayDelivery(ctx context.Context, id, idempotencyKey string) (webhooks.Delivery, bool, error) {
	hash, err := requestHash("delivery", id)
	if err != nil {
		return webhooks.Delivery{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return webhooks.Delivery{}, false, errors.Wrap(err, "beginning replay transaction")
	}
	defer func() { _ = tx.Rollback() }()
	if cached, err := replayCached[webhooks.Delivery](ctx, tx, idempotencyKey, hash); err != nil {
		return webhooks.Delivery{}, false, err
	} else if cached != nil {
		return *cached, false, errors.Wrap(tx.Commit(), "committing cached replay")
	}

	delivery := webhooks.Delivery{}
	err = tx.NewSelect().Model(&delivery).Where("id = ?", id).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return webhooks.Delivery{}, false, storage.ErrDeliveryNotFound
	}
	if err != nil {
		return webhooks.Delivery{}, false, errors.Wrap(err, "selecting delivery for replay")
	}
	config := webhooks.Config{}
	if err := tx.NewSelect().Model(&config).
		Where("id = ?", delivery.ConfigID).
		Where("active = true").
		Where("deleted_at IS NULL").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return webhooks.Delivery{}, false, storage.ErrDeliveryNotReplayable
		}
		return webhooks.Delivery{}, false, errors.Wrap(err, "selecting replay config")
	}
	now := time.Now().UTC()
	switch delivery.Status {
	case webhooks.StatusDeliveryFailed:
		delivery.Status = webhooks.StatusDeliveryPending
		delivery.ReplayGeneration++
		delivery.AttemptCount = 0
		delivery.CycleStartedAt = nil
		delivery.NextAttemptAt = &now
		delivery.ClaimedAt = nil
	case webhooks.StatusDeliveryPending:
		delivery.NextAttemptAt = &now
	default:
		return webhooks.Delivery{}, false, storage.ErrDeliveryNotReplayable
	}
	delivery.UpdatedAt = now
	if _, err := tx.NewUpdate().Model(&delivery).
		Column("status", "replay_generation", "attempt_count", "cycle_started_at", "next_attempt_at", "claimed_at", "updated_at").
		WherePK().Exec(ctx); err != nil {
		return webhooks.Delivery{}, false, errors.Wrap(err, "replaying delivery")
	}
	if err := storeReplayResponse(ctx, tx, idempotencyKey, hash, delivery); err != nil {
		return webhooks.Delivery{}, false, err
	}
	return delivery, true, errors.Wrap(tx.Commit(), "committing delivery replay")
}

func (s Store) ReplayDeliveries(ctx context.Context, request webhooks.ReplayDeliveriesRequest, idempotencyKey string) (webhooks.ReplayDeliveriesResult, bool, error) {
	hash, err := requestHash("deliveries", request)
	if err != nil {
		return webhooks.ReplayDeliveriesResult{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return webhooks.ReplayDeliveriesResult{}, false, errors.Wrap(err, "beginning bulk replay transaction")
	}
	defer func() { _ = tx.Rollback() }()
	if cached, err := replayCached[webhooks.ReplayDeliveriesResult](ctx, tx, idempotencyKey, hash); err != nil {
		return webhooks.ReplayDeliveriesResult{}, false, err
	} else if cached != nil {
		return *cached, false, errors.Wrap(tx.Commit(), "committing cached bulk replay")
	}

	if request.CreatedAtTo.IsZero() {
		request.CreatedAtTo = time.Now().UTC()
	}
	if len(request.Statuses) == 0 {
		request.Statuses = []string{webhooks.StatusDeliveryFailed, webhooks.StatusDeliveryPending}
	}
	if request.PageSize <= 0 || request.PageSize > maxReplayPageSize {
		request.PageSize = maxReplayPageSize
	}
	candidates := []webhooks.Delivery{}
	q := tx.NewSelect().Model(&candidates).ModelTableExpr("deliveries AS d").
		ColumnExpr("d.*").
		Join("JOIN configs c ON c.id = d.config_id").
		Where("d.created_at >= ?", request.CreatedAtFrom).
		Where("d.created_at <= ?", request.CreatedAtTo).
		Where("d.status IN (?)", bun.List(request.Statuses)).
		Where("c.active = true AND c.deleted_at IS NULL").
		OrderExpr("d.created_at ASC, d.id ASC").
		Limit(request.PageSize + 1).
		For("UPDATE OF d")
	if len(request.ConfigIDs) > 0 {
		q = q.Where("d.config_id IN (?)", bun.List(request.ConfigIDs))
	}
	if request.Cursor != nil {
		q = q.Where("(d.created_at, d.id) > (?, ?)", request.Cursor.CreatedAt, request.Cursor.ID)
	}
	if err := q.Scan(ctx); err != nil {
		return webhooks.ReplayDeliveriesResult{}, false, errors.Wrap(err, "selecting deliveries for bulk replay")
	}

	result := webhooks.ReplayDeliveriesResult{CreatedAtTo: request.CreatedAtTo}
	if len(candidates) > request.PageSize {
		result.HasMore = true
		candidates = candidates[:request.PageSize]
	}
	failedIDs := make([]string, 0, len(candidates))
	pendingIDs := make([]string, 0, len(candidates))
	for _, delivery := range candidates {
		switch delivery.Status {
		case webhooks.StatusDeliveryFailed:
			failedIDs = append(failedIDs, delivery.ID)
		case webhooks.StatusDeliveryPending:
			pendingIDs = append(pendingIDs, delivery.ID)
		default:
			result.Skipped++
		}
	}
	now := time.Now().UTC()
	if len(failedIDs) > 0 {
		res, err := tx.NewUpdate().Model((*webhooks.Delivery)(nil)).
			Where("id IN (?)", bun.List(failedIDs)).Where("status = ?", webhooks.StatusDeliveryFailed).
			Set("status = ?", webhooks.StatusDeliveryPending).
			Set("replay_generation = replay_generation + 1, attempt_count = 0, cycle_started_at = NULL, claimed_at = NULL").
			Set("next_attempt_at = ?, updated_at = ?", now, now).Exec(ctx)
		if err != nil {
			return webhooks.ReplayDeliveriesResult{}, false, errors.Wrap(err, "replaying failed deliveries")
		}
		affected, _ := res.RowsAffected()
		result.Replayed = int(affected)
		result.Skipped += len(failedIDs) - result.Replayed
	}
	if len(pendingIDs) > 0 {
		res, err := tx.NewUpdate().Model((*webhooks.Delivery)(nil)).
			Where("id IN (?)", bun.List(pendingIDs)).Where("status = ?", webhooks.StatusDeliveryPending).
			Set("next_attempt_at = ?, updated_at = ?", now, now).Exec(ctx)
		if err != nil {
			return webhooks.ReplayDeliveriesResult{}, false, errors.Wrap(err, "expediting pending deliveries")
		}
		affected, _ := res.RowsAffected()
		result.Expedited = int(affected)
		result.Skipped += len(pendingIDs) - result.Expedited
	}
	if result.HasMore && len(candidates) > 0 {
		last := candidates[len(candidates)-1]
		result.NextCursor = &webhooks.DeliveryCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		result.NextCursorToken, err = webhooks.EncodeReplayDeliveryCursor(webhooks.ReplayDeliveryCursor{
			Position: *result.NextCursor, CreatedAtFrom: request.CreatedAtFrom, CreatedAtTo: request.CreatedAtTo,
			Statuses: request.Statuses, ConfigIDs: request.ConfigIDs,
		})
		if err != nil {
			return webhooks.ReplayDeliveriesResult{}, false, errors.Wrap(err, "encoding bulk replay cursor")
		}
	}
	if err := storeReplayResponse(ctx, tx, idempotencyKey, hash, result); err != nil {
		return webhooks.ReplayDeliveriesResult{}, false, err
	}
	return result, true, errors.Wrap(tx.Commit(), "committing bulk replay")
}

func (s Store) PurgeFinishedDeliveries(ctx context.Context, successOlderThan, failedOlderThan time.Duration, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	var total int64
	for _, target := range []struct {
		statuses []string
		age      time.Duration
	}{
		{[]string{webhooks.StatusDeliverySucceeded}, successOlderThan},
		{[]string{webhooks.StatusDeliveryFailed, webhooks.StatusDeliveryCancelled}, failedOlderThan},
	} {
		if target.age <= 0 {
			continue
		}
		res, err := s.db.NewRaw(`
			DELETE FROM deliveries
			WHERE id IN (
				SELECT id FROM deliveries
				WHERE status IN (?) AND updated_at < ?
				ORDER BY updated_at, id
				LIMIT ? FOR UPDATE SKIP LOCKED
			)
		`, bun.List(target.statuses), time.Now().UTC().Add(-target.age), batchSize).Exec(ctx)
		if err != nil {
			return total, errors.Wrap(err, "purging deliveries")
		}
		affected, _ := res.RowsAffected()
		total += affected
	}
	_, err := s.db.NewDelete().Model((*webhooks.ReplayRequestRecord)(nil)).
		Where("created_at < ?", time.Now().UTC().Add(-24*time.Hour)).Exec(ctx)
	if err != nil {
		return total, errors.Wrap(err, "purging replay requests")
	}
	_, err = s.db.NewRaw(`
		DELETE FROM configs
		WHERE id IN (
			SELECT c.id FROM configs c
			WHERE c.deleted_at IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM deliveries d WHERE d.config_id = c.id)
			ORDER BY c.deleted_at, c.id
			LIMIT ? FOR UPDATE SKIP LOCKED
		)
	`, batchSize).Exec(ctx)
	return total, errors.Wrap(err, "purging unreferenced deleted configs")
}

func (s Store) BackfillDeliveries(ctx context.Context, successSince, failedSince time.Duration, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	webhookIDs := []string{}
	err := s.db.NewRaw(`
		SELECT a.webhook_id
		FROM attempts a
		WHERE (
			a.status IN (?, ?)
			OR (a.status = ? AND a.updated_at >= ?)
			OR (a.status = ? AND a.updated_at >= ?)
		)
		AND COALESCE(
			(SELECT d.updated_at FROM deliveries d WHERE d.event_id = 'legacy:' || a.webhook_id LIMIT 1),
			TIMESTAMPTZ 'epoch'
		) < (SELECT MAX(a2.updated_at) FROM attempts a2 WHERE a2.webhook_id = a.webhook_id)
		GROUP BY a.webhook_id
		ORDER BY MIN(a.created_at)
		LIMIT ?
	`, webhooks.StatusAttemptToRetry, webhooks.StatusAttemptRetrying,
		webhooks.StatusAttemptSuccess, time.Now().UTC().Add(-successSince),
		webhooks.StatusAttemptFailed, time.Now().UTC().Add(-failedSince), batchSize).Scan(ctx, &webhookIDs)
	if err != nil {
		return 0, errors.Wrap(err, "finding attempts to backfill")
	}
	var migrated int64
	for _, webhookID := range webhookIDs {
		if err := s.backfillWebhook(ctx, webhookID); err != nil {
			return migrated, err
		}
		migrated++
	}
	return migrated, nil
}

func (s Store) backfillWebhook(ctx context.Context, webhookID string) error {
	attempts := []webhooks.Attempt{}
	if err := s.db.NewSelect().Model(&attempts).Where("webhook_id = ?", webhookID).
		OrderExpr("created_at ASC, id ASC").Scan(ctx); err != nil {
		return errors.Wrap(err, "reading legacy attempts")
	}
	if len(attempts) == 0 {
		return nil
	}
	config := attempts[len(attempts)-1].Config
	if config.ID == "" {
		return fmt.Errorf("backfilling webhook %s: missing config ID", webhookID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "beginning backfill transaction")
	}
	defer func() { _ = tx.Rollback() }()
	currentConfig := webhooks.Config{}
	err = tx.NewSelect().Model(&currentConfig).Where("id = ?", config.ID).For("UPDATE").Scan(ctx)
	configActive := false
	if errors.Is(err, sql.ErrNoRows) {
		now := time.Now().UTC()
		config.Active = false
		config.DeletedAt = &now
		config.Secret = webhooks.NewSecret()
		if _, err := tx.NewInsert().Model(&config).Exec(ctx); err != nil {
			return errors.Wrap(err, "creating tombstone config")
		}
	} else if err != nil {
		return errors.Wrap(err, "checking backfill config")
	} else {
		config = currentConfig
		configActive = config.Active && config.DeletedAt == nil
	}

	last := attempts[len(attempts)-1]
	status := webhooks.StatusDeliveryFailed
	for _, attempt := range attempts {
		if attempt.Status == webhooks.StatusAttemptToRetry || attempt.Status == webhooks.StatusAttemptRetrying {
			status = webhooks.StatusDeliveryPending
		}
	}
	if status != webhooks.StatusDeliveryPending && last.Status == webhooks.StatusAttemptSuccess {
		status = webhooks.StatusDeliverySucceeded
	}
	if status == webhooks.StatusDeliveryPending && !configActive {
		status = webhooks.StatusDeliveryCancelled
	}
	var event publish.EventMessage
	_ = json.Unmarshal([]byte(last.Payload), &event)
	cycleStartedAt := attempts[0].CreatedAt
	lastAttemptAt := last.CreatedAt
	statusCode := last.StatusCode
	delivery := webhooks.Delivery{
		ID: webhookID, EventID: "legacy:" + webhookID, IdempotencyKey: event.IdempotencyKey,
		ConfigID: config.ID, EventType: event.Type, Payload: last.Payload, Status: status,
		AttemptCount: len(attempts), CycleStartedAt: &cycleStartedAt, LastAttemptAt: &lastAttemptAt,
		LastStatusCode: &statusCode, CreatedAt: attempts[0].CreatedAt, UpdatedAt: last.UpdatedAt,
	}
	if status == webhooks.StatusDeliveryPending {
		delivery.NextAttemptAt = &last.NextRetryAfter
	}
	if _, err := tx.NewInsert().Model(&delivery).
		On("CONFLICT (event_id, config_id) DO UPDATE").
		Set("status = EXCLUDED.status").
		Set("payload = EXCLUDED.payload, event_type = EXCLUDED.event_type, idempotency_key = EXCLUDED.idempotency_key").
		Set("attempt_count = EXCLUDED.attempt_count, cycle_started_at = EXCLUDED.cycle_started_at").
		Set("next_attempt_at = EXCLUDED.next_attempt_at, claimed_at = NULL").
		Set("last_attempt_at = EXCLUDED.last_attempt_at, last_status_code = EXCLUDED.last_status_code").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx); err != nil {
		return errors.Wrap(err, "inserting backfilled delivery")
	}
	for index, attempt := range attempts {
		outcome := webhooks.OutcomeDeliveryPermanentFailure
		switch attempt.Status {
		case webhooks.StatusAttemptSuccess:
			outcome = webhooks.OutcomeDeliverySucceeded
		case webhooks.StatusAttemptToRetry, webhooks.StatusAttemptRetrying:
			outcome = webhooks.OutcomeDeliveryRetryableFailure
		}
		record := webhooks.DeliveryAttempt{
			ID: attempt.ID, DeliveryID: delivery.ID, AttemptNumber: index + 1,
			Endpoint: attempt.Config.Endpoint, Outcome: outcome, StatusCode: attempt.StatusCode,
			CreatedAt: attempt.CreatedAt,
		}
		if _, err := tx.NewInsert().Model(&record).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
			return errors.Wrap(err, "inserting backfilled delivery attempt")
		}
	}
	return errors.Wrap(tx.Commit(), "committing backfilled delivery")
}
