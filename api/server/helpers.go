package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/golang/gddo/httputil/header"
	"github.com/google/uuid"
	"github.com/julienschmidt/httprouter"
)

type errInvalidParams struct {
	status int
	msg    string
}

func (e *errInvalidParams) Error() string {
	return e.msg
}

func validateParams(p httprouter.Params) error {
	_, err := uuid.Parse(p.ByName(userIdPathParam))
	if err != nil {
		msg := "userId path parameter should be a valid UUID"
		return &errInvalidParams{status: http.StatusBadRequest, msg: msg}
	}

	return nil
}

type errInvalidBody struct {
	status int
	msg    string
}

func (e *errInvalidBody) Error() string {
	return e.msg
}

func decodeJSONBody(r *http.Request, dst interface{}) error {
	if r.Header.Get("Content-Type") != "" {
		value, _ := header.ParseValueAndParams(r.Header, "Content-Type")
		if value != "application/json" {
			msg := "Content-Type header should be application/json"
			return &errInvalidBody{status: http.StatusUnsupportedMediaType, msg: msg}
		}
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&dst); err != nil {
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError

		switch {
		case errors.As(err, &syntaxError):
			msg := fmt.Sprintf("Request body contains badly-formed JSON (at position %d)", syntaxError.Offset)
			return &errInvalidBody{status: http.StatusBadRequest, msg: msg}

		case errors.Is(err, io.ErrUnexpectedEOF):
			msg := "Request body contains badly-formed JSON"
			return &errInvalidBody{status: http.StatusBadRequest, msg: msg}

		case errors.As(err, &unmarshalTypeError):
			msg := fmt.Sprintf("Request body contains an invalid value for the %q field (at position %d)", unmarshalTypeError.Field, unmarshalTypeError.Offset)
			return &errInvalidBody{status: http.StatusBadRequest, msg: msg}

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			fieldName := strings.TrimPrefix(err.Error(), "json: unknown field ")
			msg := fmt.Sprintf("Request body contains unknown field %s", fieldName)
			return &errInvalidBody{status: http.StatusBadRequest, msg: msg}

		case errors.Is(err, io.EOF):
			msg := "Request body must not be empty"
			return &errInvalidBody{status: http.StatusBadRequest, msg: msg}

		default:
			return err
		}
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		msg := "Request body must only contain a single JSON object"
		return &errInvalidBody{status: http.StatusBadRequest, msg: msg}
	}

	return nil
}
