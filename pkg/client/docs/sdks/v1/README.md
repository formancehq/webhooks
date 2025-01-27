# V1
(*Webhooks.V1*)

### Available Operations

* [GetManyConfigs](#getmanyconfigs) - Get many configs
* [InsertConfig](#insertconfig) - Insert a new config
* [DeleteConfig](#deleteconfig) - Delete one config
* [UpdateConfig](#updateconfig) - Update one config
* [TestConfig](#testconfig) - Test one config
* [ActivateConfig](#activateconfig) - Activate one config
* [DeactivateConfig](#deactivateconfig) - Deactivate one config
* [ChangeConfigSecret](#changeconfigsecret) - Change the signing secret of a config

## GetManyConfigs

Sorted by updated date descending

### Example Usage

```go
package main

import(
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"context"
	"log"
)

func main() {
    s := client.New(
        client.WithSecurity(components.Security{
            ClientID: "",
            ClientSecret: "",
        }),
    )
    request := operations.GetManyConfigsRequest{
        ID: client.String("4997257d-dfb6-445b-929c-cbe2ab182818"),
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

### Parameters

| Parameter                                                                            | Type                                                                                 | Required                                                                             | Description                                                                          |
| ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ |
| `ctx`                                                                                | [context.Context](https://pkg.go.dev/context#Context)                                | :heavy_check_mark:                                                                   | The context to use for the request.                                                  |
| `request`                                                                            | [operations.GetManyConfigsRequest](../../models/operations/getmanyconfigsrequest.md) | :heavy_check_mark:                                                                   | The request object to use for the request.                                           |
| `opts`                                                                               | [][operations.Option](../../models/operations/option.md)                             | :heavy_minus_sign:                                                                   | The options for this request.                                                        |


### Response

**[*operations.GetManyConfigsResponse](../../models/operations/getmanyconfigsresponse.md), error**
| Error Object            | Status Code             | Content Type            |
| ----------------------- | ----------------------- | ----------------------- |
| sdkerrors.ErrorResponse | default                 | application/json        |
| sdkerrors.SDKError      | 4xx-5xx                 | */*                     |

## InsertConfig

Insert a new webhooks config.

The endpoint should be a valid https URL and be unique.

The secret is the endpoint's verification secret.
If not passed or empty, a secret is automatically generated.
The format is a random string of bytes of size 24, base64 encoded. (larger size after encoding)

All eventTypes are converted to lower-case when inserted.


### Example Usage

```go
package main

import(
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client"
	"context"
	"log"
)

func main() {
    s := client.New(
        client.WithSecurity(components.Security{
            ClientID: "",
            ClientSecret: "",
        }),
    )
    request := components.ConfigUser{
        Name: client.String("customer_payment"),
        Endpoint: "https://example.com",
        Secret: client.String("V0bivxRWveaoz08afqjU6Ko/jwO0Cb+3"),
        EventTypes: []string{
            "TYPE1",
            "TYPE2",
        },
    }
    ctx := context.Background()
    res, err := s.Webhooks.V1.InsertConfig(ctx, request)
    if err != nil {
        log.Fatal(err)
    }
    if res.ConfigResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                      | Type                                                           | Required                                                       | Description                                                    |
| -------------------------------------------------------------- | -------------------------------------------------------------- | -------------------------------------------------------------- | -------------------------------------------------------------- |
| `ctx`                                                          | [context.Context](https://pkg.go.dev/context#Context)          | :heavy_check_mark:                                             | The context to use for the request.                            |
| `request`                                                      | [components.ConfigUser](../../models/components/configuser.md) | :heavy_check_mark:                                             | The request object to use for the request.                     |
| `opts`                                                         | [][operations.Option](../../models/operations/option.md)       | :heavy_minus_sign:                                             | The options for this request.                                  |


### Response

**[*operations.InsertConfigResponse](../../models/operations/insertconfigresponse.md), error**
| Error Object            | Status Code             | Content Type            |
| ----------------------- | ----------------------- | ----------------------- |
| sdkerrors.ErrorResponse | default                 | application/json        |
| sdkerrors.SDKError      | 4xx-5xx                 | */*                     |

## DeleteConfig

Delete a webhooks config by ID.

### Example Usage

```go
package main

import(
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"context"
	"log"
)

func main() {
    s := client.New(
        client.WithSecurity(components.Security{
            ClientID: "",
            ClientSecret: "",
        }),
    )
    request := operations.DeleteConfigRequest{
        ID: "4997257d-dfb6-445b-929c-cbe2ab182818",
    }
    ctx := context.Background()
    res, err := s.Webhooks.V1.DeleteConfig(ctx, request)
    if err != nil {
        log.Fatal(err)
    }
    if res != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                        | Type                                                                             | Required                                                                         | Description                                                                      |
| -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `ctx`                                                                            | [context.Context](https://pkg.go.dev/context#Context)                            | :heavy_check_mark:                                                               | The context to use for the request.                                              |
| `request`                                                                        | [operations.DeleteConfigRequest](../../models/operations/deleteconfigrequest.md) | :heavy_check_mark:                                                               | The request object to use for the request.                                       |
| `opts`                                                                           | [][operations.Option](../../models/operations/option.md)                         | :heavy_minus_sign:                                                               | The options for this request.                                                    |


### Response

**[*operations.DeleteConfigResponse](../../models/operations/deleteconfigresponse.md), error**
| Error Object            | Status Code             | Content Type            |
| ----------------------- | ----------------------- | ----------------------- |
| sdkerrors.ErrorResponse | default                 | application/json        |
| sdkerrors.SDKError      | 4xx-5xx                 | */*                     |

## UpdateConfig

Update a webhooks config by ID.

### Example Usage

```go
package main

import(
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"context"
	"log"
)

func main() {
    s := client.New(
        client.WithSecurity(components.Security{
            ClientID: "",
            ClientSecret: "",
        }),
    )
    request := operations.UpdateConfigRequest{
        ID: "4997257d-dfb6-445b-929c-cbe2ab182818",
        ConfigUser: components.ConfigUser{
            Name: client.String("customer_payment"),
            Endpoint: "https://example.com",
            Secret: client.String("V0bivxRWveaoz08afqjU6Ko/jwO0Cb+3"),
            EventTypes: []string{
                "TYPE1",
                "TYPE2",
            },
        },
    }
    ctx := context.Background()
    res, err := s.Webhooks.V1.UpdateConfig(ctx, request)
    if err != nil {
        log.Fatal(err)
    }
    if res != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                        | Type                                                                             | Required                                                                         | Description                                                                      |
| -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `ctx`                                                                            | [context.Context](https://pkg.go.dev/context#Context)                            | :heavy_check_mark:                                                               | The context to use for the request.                                              |
| `request`                                                                        | [operations.UpdateConfigRequest](../../models/operations/updateconfigrequest.md) | :heavy_check_mark:                                                               | The request object to use for the request.                                       |
| `opts`                                                                           | [][operations.Option](../../models/operations/option.md)                         | :heavy_minus_sign:                                                               | The options for this request.                                                    |


### Response

**[*operations.UpdateConfigResponse](../../models/operations/updateconfigresponse.md), error**
| Error Object            | Status Code             | Content Type            |
| ----------------------- | ----------------------- | ----------------------- |
| sdkerrors.ErrorResponse | default                 | application/json        |
| sdkerrors.SDKError      | 4xx-5xx                 | */*                     |

## TestConfig

Test a config by sending a webhook to its endpoint.

### Example Usage

```go
package main

import(
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"context"
	"log"
)

func main() {
    s := client.New(
        client.WithSecurity(components.Security{
            ClientID: "",
            ClientSecret: "",
        }),
    )
    request := operations.TestConfigRequest{
        ID: "4997257d-dfb6-445b-929c-cbe2ab182818",
    }
    ctx := context.Background()
    res, err := s.Webhooks.V1.TestConfig(ctx, request)
    if err != nil {
        log.Fatal(err)
    }
    if res.AttemptResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                    | Type                                                                         | Required                                                                     | Description                                                                  |
| ---------------------------------------------------------------------------- | ---------------------------------------------------------------------------- | ---------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| `ctx`                                                                        | [context.Context](https://pkg.go.dev/context#Context)                        | :heavy_check_mark:                                                           | The context to use for the request.                                          |
| `request`                                                                    | [operations.TestConfigRequest](../../models/operations/testconfigrequest.md) | :heavy_check_mark:                                                           | The request object to use for the request.                                   |
| `opts`                                                                       | [][operations.Option](../../models/operations/option.md)                     | :heavy_minus_sign:                                                           | The options for this request.                                                |


### Response

**[*operations.TestConfigResponse](../../models/operations/testconfigresponse.md), error**
| Error Object            | Status Code             | Content Type            |
| ----------------------- | ----------------------- | ----------------------- |
| sdkerrors.ErrorResponse | default                 | application/json        |
| sdkerrors.SDKError      | 4xx-5xx                 | */*                     |

## ActivateConfig

Activate a webhooks config by ID, to start receiving webhooks to its endpoint.

### Example Usage

```go
package main

import(
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"context"
	"log"
)

func main() {
    s := client.New(
        client.WithSecurity(components.Security{
            ClientID: "",
            ClientSecret: "",
        }),
    )
    request := operations.ActivateConfigRequest{
        ID: "4997257d-dfb6-445b-929c-cbe2ab182818",
    }
    ctx := context.Background()
    res, err := s.Webhooks.V1.ActivateConfig(ctx, request)
    if err != nil {
        log.Fatal(err)
    }
    if res.ConfigResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                            | Type                                                                                 | Required                                                                             | Description                                                                          |
| ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ |
| `ctx`                                                                                | [context.Context](https://pkg.go.dev/context#Context)                                | :heavy_check_mark:                                                                   | The context to use for the request.                                                  |
| `request`                                                                            | [operations.ActivateConfigRequest](../../models/operations/activateconfigrequest.md) | :heavy_check_mark:                                                                   | The request object to use for the request.                                           |
| `opts`                                                                               | [][operations.Option](../../models/operations/option.md)                             | :heavy_minus_sign:                                                                   | The options for this request.                                                        |


### Response

**[*operations.ActivateConfigResponse](../../models/operations/activateconfigresponse.md), error**
| Error Object            | Status Code             | Content Type            |
| ----------------------- | ----------------------- | ----------------------- |
| sdkerrors.ErrorResponse | default                 | application/json        |
| sdkerrors.SDKError      | 4xx-5xx                 | */*                     |

## DeactivateConfig

Deactivate a webhooks config by ID, to stop receiving webhooks to its endpoint.

### Example Usage

```go
package main

import(
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"context"
	"log"
)

func main() {
    s := client.New(
        client.WithSecurity(components.Security{
            ClientID: "",
            ClientSecret: "",
        }),
    )
    request := operations.DeactivateConfigRequest{
        ID: "4997257d-dfb6-445b-929c-cbe2ab182818",
    }
    ctx := context.Background()
    res, err := s.Webhooks.V1.DeactivateConfig(ctx, request)
    if err != nil {
        log.Fatal(err)
    }
    if res.ConfigResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                                | Type                                                                                     | Required                                                                                 | Description                                                                              |
| ---------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `ctx`                                                                                    | [context.Context](https://pkg.go.dev/context#Context)                                    | :heavy_check_mark:                                                                       | The context to use for the request.                                                      |
| `request`                                                                                | [operations.DeactivateConfigRequest](../../models/operations/deactivateconfigrequest.md) | :heavy_check_mark:                                                                       | The request object to use for the request.                                               |
| `opts`                                                                                   | [][operations.Option](../../models/operations/option.md)                                 | :heavy_minus_sign:                                                                       | The options for this request.                                                            |


### Response

**[*operations.DeactivateConfigResponse](../../models/operations/deactivateconfigresponse.md), error**
| Error Object            | Status Code             | Content Type            |
| ----------------------- | ----------------------- | ----------------------- |
| sdkerrors.ErrorResponse | default                 | application/json        |
| sdkerrors.SDKError      | 4xx-5xx                 | */*                     |

## ChangeConfigSecret

Change the signing secret of the endpoint of a webhooks config.

If not passed or empty, a secret is automatically generated.
The format is a random string of bytes of size 24, base64 encoded. (larger size after encoding)


### Example Usage

```go
package main

import(
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"context"
	"log"
)

func main() {
    s := client.New(
        client.WithSecurity(components.Security{
            ClientID: "",
            ClientSecret: "",
        }),
    )
    request := operations.ChangeConfigSecretRequest{
        ID: "4997257d-dfb6-445b-929c-cbe2ab182818",
        ConfigChangeSecret: &components.ConfigChangeSecret{
            Secret: "V0bivxRWveaoz08afqjU6Ko/jwO0Cb+3",
        },
    }
    ctx := context.Background()
    res, err := s.Webhooks.V1.ChangeConfigSecret(ctx, request)
    if err != nil {
        log.Fatal(err)
    }
    if res.ConfigResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                                    | Type                                                                                         | Required                                                                                     | Description                                                                                  |
| -------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `ctx`                                                                                        | [context.Context](https://pkg.go.dev/context#Context)                                        | :heavy_check_mark:                                                                           | The context to use for the request.                                                          |
| `request`                                                                                    | [operations.ChangeConfigSecretRequest](../../models/operations/changeconfigsecretrequest.md) | :heavy_check_mark:                                                                           | The request object to use for the request.                                                   |
| `opts`                                                                                       | [][operations.Option](../../models/operations/option.md)                                     | :heavy_minus_sign:                                                                           | The options for this request.                                                                |


### Response

**[*operations.ChangeConfigSecretResponse](../../models/operations/changeconfigsecretresponse.md), error**
| Error Object            | Status Code             | Content Type            |
| ----------------------- | ----------------------- | ----------------------- |
| sdkerrors.ErrorResponse | default                 | application/json        |
| sdkerrors.SDKError      | 4xx-5xx                 | */*                     |
