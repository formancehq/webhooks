package webhooks

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

func EncodeDeliveryCursor(cursor *DeliveryCursor) (string, error) {
	if cursor == nil {
		return "", nil
	}
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeDeliveryCursor(value string) (*DeliveryCursor, error) {
	if value == "" {
		return nil, nil
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor: %w", err)
	}
	cursor := DeliveryCursor{}
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.ID == "" || cursor.CreatedAt.IsZero() {
		return nil, fmt.Errorf("invalid cursor")
	}
	return &cursor, nil
}

func EncodeReplayDeliveryCursor(cursor ReplayDeliveryCursor) (string, error) {
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeReplayDeliveryCursor(value string) (*ReplayDeliveryCursor, error) {
	if value == "" {
		return nil, nil
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid replay cursor: %w", err)
	}
	cursor := ReplayDeliveryCursor{}
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.Position.ID == "" ||
		cursor.Position.CreatedAt.IsZero() || cursor.CreatedAtFrom.IsZero() || cursor.CreatedAtTo.IsZero() {
		return nil, fmt.Errorf("invalid replay cursor")
	}
	return &cursor, nil
}
