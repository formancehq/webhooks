# WebhooksConfig


## Fields

| Field                                     | Type                                      | Required                                  | Description                               | Example                                   |
| ----------------------------------------- | ----------------------------------------- | ----------------------------------------- | ----------------------------------------- | ----------------------------------------- |
| `ID`                                      | *string*                                  | :heavy_check_mark:                        | N/A                                       |                                           |
| `Endpoint`                                | *string*                                  | :heavy_check_mark:                        | N/A                                       | https://example.com                       |
| `Secret`                                  | *string*                                  | :heavy_check_mark:                        | N/A                                       | V0bivxRWveaoz08afqjU6Ko/jwO0Cb+3          |
| `EventTypes`                              | []*string*                                | :heavy_check_mark:                        | N/A                                       | [<br/>"TYPE1",<br/>"TYPE2"<br/>]          |
| `Active`                                  | *bool*                                    | :heavy_check_mark:                        | N/A                                       | true                                      |
| `CreatedAt`                               | [time.Time](https://pkg.go.dev/time#Time) | :heavy_check_mark:                        | N/A                                       |                                           |
| `UpdatedAt`                               | [time.Time](https://pkg.go.dev/time#Time) | :heavy_check_mark:                        | N/A                                       |                                           |