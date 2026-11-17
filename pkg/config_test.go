package webhooks

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate(t *testing.T) {
	cfg := ConfigUser{
		Endpoint:   "https://example.com",
		EventTypes: []string{"TYPE1", "TYPE2"},
	}
	assert.NoError(t, cfg.Validate())

	cfg = ConfigUser{
		Endpoint:   "https://example.com",
		Secret:     NewSecret(),
		EventTypes: []string{"TYPE1", "TYPE2"},
	}
	assert.NoError(t, cfg.Validate())

	cfg = ConfigUser{
		Endpoint:   " http://invalid",
		EventTypes: []string{"TYPE1", "TYPE2"},
	}
	assert.Error(t, cfg.Validate())

	cfg = ConfigUser{
		Endpoint:   "https://example.com",
		EventTypes: []string{"TYPE1", ""},
	}
	assert.Error(t, cfg.Validate())

	cfg = ConfigUser{
		Endpoint:   "https://example.com",
		Secret:     base64.StdEncoding.EncodeToString([]byte(`invalid`)),
		EventTypes: []string{"TYPE1", "TYPE2"},
	}
	assert.Error(t, cfg.Validate())
}

func TestConfigUser_ValidateForUpdate(t *testing.T) {
	// omitted/empty secret stays empty on update: the storage layer preserves
	// the stored secret instead of generating a new one
	cfg := ConfigUser{
		Endpoint:   "https://example.com",
		EventTypes: []string{"TYPE1"},
	}
	assert.NoError(t, cfg.ValidateForUpdate())
	assert.Empty(t, cfg.Secret, "update with omitted secret must not generate a secret")

	cfg = ConfigUser{
		Endpoint:   "https://example.com",
		Secret:     "",
		EventTypes: []string{"TYPE1"},
	}
	assert.NoError(t, cfg.ValidateForUpdate())
	assert.Empty(t, cfg.Secret, "update with empty secret must not generate a secret")

	// a supplied secret is kept and validated
	valid := NewSecret()
	cfg = ConfigUser{
		Endpoint:   "https://example.com",
		Secret:     valid,
		EventTypes: []string{"TYPE1"},
	}
	assert.NoError(t, cfg.ValidateForUpdate())
	assert.Equal(t, valid, cfg.Secret)

	// invalid supplied secrets are rejected
	cfg = ConfigUser{
		Endpoint:   "https://example.com",
		Secret:     "invalid",
		EventTypes: []string{"TYPE1"},
	}
	assert.Error(t, cfg.ValidateForUpdate())

	cfg = ConfigUser{
		Endpoint:   "https://example.com",
		Secret:     base64.StdEncoding.EncodeToString([]byte(`invalid`)),
		EventTypes: []string{"TYPE1"},
	}
	assert.Error(t, cfg.ValidateForUpdate())
}

func TestConfigUser_Validate_GeneratesSecretOnCreate(t *testing.T) {
	cfg := ConfigUser{
		Endpoint:   "https://example.com",
		EventTypes: []string{"TYPE1"},
	}
	assert.NoError(t, cfg.Validate())
	assert.NotEmpty(t, cfg.Secret, "creation with omitted secret must generate one")
}

// Request and storage encoding retain supplied keys; API response models
// enforce the disclosure boundary.
func TestConfigRequestAndStorageJSONPreserveSecret(t *testing.T) {
	secret := NewSecret()
	cfg := Config{
		ConfigUser: ConfigUser{
			Endpoint:   "https://example.com",
			Secret:     secret,
			EventTypes: []string{"TYPE1"},
		},
		ID:     "cfg-1",
		Active: true,
	}

	out, err := json.Marshal(cfg)
	require.NoError(t, err)
	var stored Config
	require.NoError(t, json.Unmarshal(out, &stored))
	assert.True(t, secret == stored.Secret, "storage snapshot encoding must retain the signing key")

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out, &decoded))
	assert.Equal(t, "https://example.com", decoded["endpoint"])
	assert.Equal(t, "cfg-1", decoded["id"])
	assert.Equal(t, true, decoded["active"])

	// the request direction must keep decoding the secret
	user := ConfigUser{}
	require.NoError(t, json.Unmarshal([]byte(`{"endpoint":"https://example.com","secret":"`+secret+`","eventTypes":["TYPE1"]}`), &user))
	assert.Equal(t, secret, user.Secret, "secret must remain decodable from request bodies")
	request, err := json.Marshal(user)
	require.NoError(t, err)
	var roundTrip ConfigUser
	require.NoError(t, json.Unmarshal(request, &roundTrip))
	assert.True(t, secret == roundTrip.Secret, "request encoding must preserve a supplied secret")
}

func TestConfigResponseMapping(t *testing.T) {
	secret := NewSecret()
	cfg := Config{
		ConfigUser: ConfigUser{
			Endpoint:   "https://example.com",
			Secret:     secret,
			EventTypes: []string{"TYPE1"},
		},
		ID:     "cfg-1",
		Name:   "billing events",
		Active: true,
	}

	resp := NewConfigResponse(cfg)
	out, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(out), secret)
	assert.NotContains(t, string(out), `"secret"`)
	assert.Equal(t, "cfg-1", resp.ID)
	assert.Equal(t, cfg.Name, resp.Name)
	assert.Equal(t, "https://example.com", resp.Endpoint)

	withSecret := NewConfigWithSecret(cfg)
	out, err = json.Marshal(withSecret)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"secret":"`+secret+`"`, "creation/rotation representation must carry the secret")
}
