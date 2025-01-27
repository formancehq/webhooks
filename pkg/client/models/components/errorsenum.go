// Code generated by Speakeasy (https://speakeasy.com). DO NOT EDIT.

package components

import (
	"encoding/json"
	"fmt"
)

type ErrorsEnum string

const (
	ErrorsEnumInternal   ErrorsEnum = "INTERNAL"
	ErrorsEnumValidation ErrorsEnum = "VALIDATION"
	ErrorsEnumNotFound   ErrorsEnum = "NOT_FOUND"
)

func (e ErrorsEnum) ToPointer() *ErrorsEnum {
	return &e
}
func (e *ErrorsEnum) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch v {
	case "INTERNAL":
		fallthrough
	case "VALIDATION":
		fallthrough
	case "NOT_FOUND":
		*e = ErrorsEnum(v)
		return nil
	default:
		return fmt.Errorf("invalid value for ErrorsEnum: %v", v)
	}
}
