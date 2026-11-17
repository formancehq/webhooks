package client_test

import (
	"encoding/json"
	"testing"

	"github.com/formancehq/webhooks/pkg/client/models/components"
)

func TestConfigurationRepresentations(t *testing.T) {
	// Local fixture; never a deployed signing key.
	const fixture = `{"data":{"id":"local-config","name":"billing events","endpoint":"http://localhost/hook","eventTypes":["event"],"active":true,"createdAt":"2026-11-16T12:00:00Z","updatedAt":"2026-11-16T12:00:00Z","secret":"local-fake-key"}}`
	var revealed components.ConfigWithSecretResponse
	if err := json.Unmarshal([]byte(fixture), &revealed); err != nil {
		t.Fatal(err)
	}
	if revealed.Data.ID != "local-config" || revealed.Data.Name != "billing events" || revealed.Data.Secret != "local-fake-key" {
		t.Fatal("creation/rotation decoder lost configuration fields or the intended key")
	}
	encoded, err := json.Marshal(revealed)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip components.ConfigWithSecretResponse
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Data.ID != revealed.Data.ID || roundTrip.Data.Secret != revealed.Data.Secret {
		t.Fatal("creation/rotation representation does not round-trip")
	}
	var ordinary components.ConfigResponse
	if err := json.Unmarshal([]byte(fixture), &ordinary); err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw.Data["secret"]; present {
		t.Fatal("ordinary configuration serializes a secret field")
	}
	if raw.Data["name"] != "billing events" {
		t.Fatal("ordinary configuration lost its name")
	}
	secret := "local-fake-key"
	request, err := json.Marshal(components.ConfigUser{Secret: &secret})
	if err != nil {
		t.Fatal(err)
	}
	var decoded components.ConfigUser
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Secret == nil || *decoded.Secret != secret {
		t.Fatal("input encoding lost the supplied key")
	}
}
