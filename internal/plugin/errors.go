package plugin

import (
	"errors"
	"fmt"
	"strings"
)

type ErrorCode string

const (
	ErrorCodeForbidden     ErrorCode = "FORBIDDEN"
	ErrorCodeNotFound      ErrorCode = "NOT_FOUND"
	ErrorCodeInvalidParams ErrorCode = "INVALID_PARAMS"
	ErrorCodeInternal      ErrorCode = "INTERNAL"
)

type StructuredError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *StructuredError) Error() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, msg)
}

func NewStructuredError(code ErrorCode, message string) *StructuredError {
	return &StructuredError{Code: code, Message: strings.TrimSpace(message)}
}

func AsStructuredError(err error) *StructuredError {
	var out *StructuredError
	if errors.As(err, &out) {
		return out
	}
	return nil
}

func NormalizeStructuredError(err error, fallback ErrorCode) *StructuredError {
	if err == nil {
		return nil
	}
	if se := AsStructuredError(err); se != nil {
		return se
	}
	return NewStructuredError(fallback, err.Error())
}
