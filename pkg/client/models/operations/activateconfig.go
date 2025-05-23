// Code generated by Speakeasy (https://speakeasy.com). DO NOT EDIT.

package operations

import (
	"github.com/formancehq/webhooks/pkg/client/models/components"
)

type ActivateConfigRequest struct {
	// Config ID
	ID string `pathParam:"style=simple,explode=false,name=id"`
}

func (o *ActivateConfigRequest) GetID() string {
	if o == nil {
		return ""
	}
	return o.ID
}

type ActivateConfigResponse struct {
	HTTPMeta components.HTTPMetadata `json:"-"`
	// Config successfully activated.
	ConfigResponse *components.ConfigResponse
}

func (o *ActivateConfigResponse) GetHTTPMeta() components.HTTPMetadata {
	if o == nil {
		return components.HTTPMetadata{}
	}
	return o.HTTPMeta
}

func (o *ActivateConfigResponse) GetConfigResponse() *components.ConfigResponse {
	if o == nil {
		return nil
	}
	return o.ConfigResponse
}
