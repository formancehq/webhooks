// Code generated by Speakeasy (https://speakeasy.com). DO NOT EDIT.

package components

type Cursor struct {
	HasMore bool             `json:"hasMore"`
	Data    []WebhooksConfig `json:"data"`
}

func (o *Cursor) GetHasMore() bool {
	if o == nil {
		return false
	}
	return o.HasMore
}

func (o *Cursor) GetData() []WebhooksConfig {
	if o == nil {
		return []WebhooksConfig{}
	}
	return o.Data
}

type ConfigsResponse struct {
	Cursor Cursor `json:"cursor"`
}

func (o *ConfigsResponse) GetCursor() Cursor {
	if o == nil {
		return Cursor{}
	}
	return o.Cursor
}
