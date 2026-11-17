package webhooks

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

// Config is the stored representation of a webhook configuration. It embeds
// ConfigUser, whose Secret is the stored HMAC signing key. The Secret is
// available to storage and the signer: use ConfigResponse (which omits it)
// for ordinary read/update/activation responses, and ConfigWithSecret (which
// carries it) only for the one-time disclosure on creation and secret
// rotation.
type Config struct {
	bun.BaseModel `bun:"table:configs"`

	ConfigUser

	ID        string     `json:"id" bun:",pk"`
	Active    bool       `json:"active"`
	Name      string     `json:"name" bun:"name,nullzero"`
	CreatedAt time.Time  `json:"createdAt" bun:"created_at,nullzero,notnull,default:current_timestamp"`
	UpdatedAt time.Time  `json:"updatedAt" bun:"updated_at,nullzero,notnull,default:current_timestamp"`
	DeletedAt *time.Time `json:"-" bun:"deleted_at"`
}

// ConfigUser is the request body of POST /configs and PUT /configs/{id}.
//
// Secret field semantics on update (PUT /configs/{id}):
//   - omitted (absent from the JSON body) or null: the stored secret is
//     preserved unchanged (no rotation, no generation);
//   - empty string "": same as omitted, the stored secret is preserved;
//   - non-empty: must be base64 of exactly 24 bytes and replaces the stored
//     secret (an explicit rotation).
//
// On creation (POST /configs), an omitted, null or empty secret means a new
// secret is generated. Requests retain normal JSON encoding and decoding;
// response models control disclosure.
type ConfigUser struct {
	Endpoint   string   `json:"endpoint"`
	Secret     string   `json:"secret"`
	EventTypes []string `json:"eventTypes" bun:"event_types,array"`
}

// ConfigResponse is the ordinary configuration representation returned by
// reads and by update/activate/deactivate: it never contains the stored
// signing secret.
type ConfigResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Endpoint   string    `json:"endpoint"`
	EventTypes []string  `json:"eventTypes"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// NewConfigResponse maps a stored Config to its secret-free response form.
func NewConfigResponse(c Config) ConfigResponse {
	return ConfigResponse{
		ID:         c.ID,
		Name:       c.Name,
		Endpoint:   c.Endpoint,
		EventTypes: c.EventTypes,
		Active:     c.Active,
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
}

// ConfigWithSecret is the creation/rotation representation: identical to
// ConfigResponse plus the secret, disclosed exactly once, at the moment the
// secret is created (POST /configs) or rotated
// (PUT /configs/{id}/secret/change).
type ConfigWithSecret struct {
	ConfigResponse
	Secret string `json:"secret"`
}

// NewConfigWithSecret maps a stored Config to its one-time-disclosure form.
func NewConfigWithSecret(c Config) ConfigWithSecret {
	return ConfigWithSecret{
		ConfigResponse: NewConfigResponse(c),
		Secret:         c.Secret,
	}
}

// NewConfigResponses maps stored Configs to their secret-free response form.
func NewConfigResponses(cfgs []Config) []ConfigResponse {
	res := make([]ConfigResponse, len(cfgs))
	for i, c := range cfgs {
		res[i] = NewConfigResponse(c)
	}
	return res
}

// NewConfigResponsePtr is NewConfigResponse for a single value address.
func NewConfigResponsePtr(c Config) *ConfigResponse {
	res := NewConfigResponse(c)
	return &res
}

// NewConfigWithSecretPtr is NewConfigWithSecret for a single value address.
func NewConfigWithSecretPtr(c Config) *ConfigWithSecret {
	res := NewConfigWithSecret(c)
	return &res
}

func NewConfig(cfgUser ConfigUser) Config {
	return Config{
		ConfigUser: cfgUser,
		ID:         uuid.NewString(),
		Active:     true,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
}

var (
	ErrInvalidEndpoint   = errors.New("endpoint should be a valid url")
	ErrInvalidEventTypes = errors.New("eventTypes should be filled")
	ErrInvalidSecret     = errors.New("decoded secret should be of size 24")
)

// Validate checks a ConfigUser for creation (POST /configs): an omitted or
// empty secret is replaced by a freshly generated one, a supplied secret is
// validated (base64 of exactly 24 bytes).
func (c *ConfigUser) Validate() error {
	if err := c.validate(); err != nil {
		return err
	}

	if c.Secret == "" {
		c.Secret = NewSecret()
	} else if err := validateSecret(c.Secret); err != nil {
		return err
	}

	return nil
}

// ValidateForUpdate checks a ConfigUser for an update (PUT /configs/{id}).
// Unlike creation, an omitted or empty secret is left empty: the storage
// layer then preserves the stored secret instead of rotating it. A supplied
// non-empty secret is validated and will replace the stored one.
func (c *ConfigUser) ValidateForUpdate() error {
	if err := c.validate(); err != nil {
		return err
	}

	if c.Secret != "" {
		if err := validateSecret(c.Secret); err != nil {
			return err
		}
	}

	return nil
}

func validateSecret(secret string) error {
	decoded, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return fmt.Errorf("secret should be base64 encoded: %w", err)
	}
	if len(decoded) != 24 {
		return ErrInvalidSecret
	}
	return nil
}

func (c *ConfigUser) validate() error {
	if u, err := url.Parse(c.Endpoint); err != nil || len(u.String()) == 0 {
		return ErrInvalidEndpoint
	}

	if len(c.EventTypes) == 0 {
		return ErrInvalidEventTypes
	}

	for i, t := range c.EventTypes {
		if len(t) == 0 {
			return ErrInvalidEventTypes
		}
		c.EventTypes[i] = strings.ToLower(t)
	}

	return nil
}
