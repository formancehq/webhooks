<!-- Start SDK Example Usage [usage] -->
```go
package main

import (
	"context"
	"github.com/formancehq/webhooks/pkg/client"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"log"
)

func main() {
	s := client.New(
		client.WithSecurity(components.Security{
			ClientID:     "",
			ClientSecret: "",
		}),
	)
	request := operations.GetManyConfigsRequest{
		ID:       client.String("4997257d-dfb6-445b-929c-cbe2ab182818"),
		Endpoint: client.String("https://example.com"),
	}
	ctx := context.Background()
	res, err := s.Webhooks.V1.GetManyConfigs(ctx, request)
	if err != nil {
		log.Fatal(err)
	}
	if res.ConfigsResponse != nil {
		// handle response
	}
}

```
<!-- End SDK Example Usage [usage] -->