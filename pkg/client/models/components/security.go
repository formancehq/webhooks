// Code generated by Speakeasy (https://speakeasy.com). DO NOT EDIT.

package components

import (
	"github.com/formancehq/webhooks/pkg/client/internal/utils"
)

type Security struct {
	ClientID     string `security:"scheme,type=oauth2,subtype=client_credentials,name=clientID"`
	ClientSecret string `security:"scheme,type=oauth2,subtype=client_credentials,name=clientSecret"`
	tokenURL     string `const:"/oauth/token"`
}

func (s Security) MarshalJSON() ([]byte, error) {
	return utils.MarshalJSON(s, "", false)
}

func (s *Security) UnmarshalJSON(data []byte) error {
	if err := utils.UnmarshalJSON(data, &s, "", false, false); err != nil {
		return err
	}
	return nil
}

func (o *Security) GetClientID() string {
	if o == nil {
		return ""
	}
	return o.ClientID
}

func (o *Security) GetClientSecret() string {
	if o == nil {
		return ""
	}
	return o.ClientSecret
}

func (o *Security) GetTokenURL() string {
	return "/oauth/token"
}
