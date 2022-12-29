# Cursor

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**HasMore** | **bool** |  | 
**Data** | [**[]Config**](Config.md) |  | 

## Methods

### NewCursor

`func NewCursor(hasMore bool, data []Config, ) *Cursor`

NewCursor instantiates a new Cursor object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewCursorWithDefaults

`func NewCursorWithDefaults() *Cursor`

NewCursorWithDefaults instantiates a new Cursor object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetHasMore

`func (o *Cursor) GetHasMore() bool`

GetHasMore returns the HasMore field if non-nil, zero value otherwise.

### GetHasMoreOk

`func (o *Cursor) GetHasMoreOk() (*bool, bool)`

GetHasMoreOk returns a tuple with the HasMore field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetHasMore

`func (o *Cursor) SetHasMore(v bool)`

SetHasMore sets HasMore field to given value.


### GetData

`func (o *Cursor) GetData() []Config`

GetData returns the Data field if non-nil, zero value otherwise.

### GetDataOk

`func (o *Cursor) GetDataOk() (*[]Config, bool)`

GetDataOk returns a tuple with the Data field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetData

`func (o *Cursor) SetData(v []Config)`

SetData sets Data field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


