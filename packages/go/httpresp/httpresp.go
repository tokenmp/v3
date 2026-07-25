// Package httpresp provides a unified JSON response envelope
// {code, data, message} for all TokenMP v3 services except the Executor
// protocol-passthrough endpoints (/v1/* which must preserve OpenAI/Anthropic
// native format).
//
//   - Success (200/201): { "code": 0, "data": <payload>, "message": "success" }
//   - Paginated (200):   { "code": 0, "data": { "items": [...], "total": N, "page": P, "pageSize": S }, "message": "success" }
//   - Error (4xx/5xx):   { "code": <non-zero>, "data": null, "message": "..." }
//   - 204 No-Content:    no body (HTTP 204)
//
// HTTP status codes keep their standard semantics. The numeric code in the
// body provides a finer-grained, machine-readable error identifier.
package httpresp

import (
	"encoding/json"
	"net/http"
)

// Code is the numeric status code in the response envelope.
// 0 means success; any non-zero value indicates an error.
type Code int

const (
	// CodeOK is the success code.
	CodeOK Code = 0

	// ---- Client errors (4xx) ----
	CodeBadRequest         Code = 1000
	CodeInvalidCredentials Code = 1001
	CodeEmailTaken         Code = 1002
	CodePasswordTooWeak    Code = 1003
	CodeInvalidEmail       Code = 1004
	CodeInvalidToken       Code = 1005
	CodeInvalidRefresh     Code = 1006
	CodeUnauthorized       Code = 1007
	CodeForbidden          Code = 1008
	CodeNotFound           Code = 1009
	CodeConflict           Code = 1010
	CodeInvalidJSON        Code = 1014
	CodeMissingField       Code = 1015

	// ---- Server errors (5xx) ----
	CodeInternalError      Code = 1011
	CodeServiceUnavailable Code = 1012
	CodeNotReady           Code = 1013
	CodeBadGateway         Code = 1016
	CodeNotImplemented     Code = 1017
)

// ErrCode maps a Code to its string identifier (for backward compatibility
// with existing frontend error messages).
func (c Code) String() string {
	switch c {
	case CodeOK:
		return "ok"
	case CodeBadRequest:
		return "bad_request"
	case CodeInvalidCredentials:
		return "invalid_credentials"
	case CodeEmailTaken:
		return "email_taken"
	case CodePasswordTooWeak:
		return "password_too_weak"
	case CodeInvalidEmail:
		return "invalid_email"
	case CodeInvalidToken:
		return "invalid_token"
	case CodeInvalidRefresh:
		return "invalid_refresh_token"
	case CodeUnauthorized:
		return "unauthorized"
	case CodeForbidden:
		return "forbidden"
	case CodeNotFound:
		return "not_found"
	case CodeConflict:
		return "conflict"
	case CodeInternalError:
		return "internal_error"
	case CodeServiceUnavailable:
		return "service_unavailable"
	case CodeNotReady:
		return "not_ready"
	case CodeBadGateway:
		return "bad_gateway"
	case CodeInvalidJSON:
		return "invalid_json"
	case CodeMissingField:
		return "missing_field"
	case CodeNotImplemented:
		return "not_implemented"
	default:
		return "error"
	}
}

// HTTPStatus maps a Code to its canonical HTTP status code.
func (c Code) HTTPStatus() int {
	switch c {
	case CodeOK:
		return http.StatusOK
	case CodeBadRequest, CodePasswordTooWeak, CodeInvalidEmail, CodeInvalidJSON, CodeMissingField:
		return http.StatusBadRequest
	case CodeInvalidCredentials, CodeInvalidToken, CodeInvalidRefresh, CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeEmailTaken, CodeConflict:
		return http.StatusConflict
	case CodeNotReady, CodeServiceUnavailable:
		return http.StatusServiceUnavailable
	case CodeBadGateway:
		return http.StatusBadGateway
	case CodeNotImplemented:
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

// envelope is the JSON response wrapper.
type envelope struct {
	Code    Code   `json:"code"`
	Data    any    `json:"data"`
	Message string `json:"message"`
}

// Page is the standard pagination payload inside data.
type Page[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// OK writes a 200 response with the given data.
func OK(w http.ResponseWriter, data any) {
	write(w, http.StatusOK, CodeOK, data, "success")
}

// Created writes a 201 response with the given data.
func Created(w http.ResponseWriter, data any) {
	write(w, http.StatusCreated, CodeOK, data, "success")
}

// NoContent writes a 204 response with no body.
func NoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// OKPage writes a 200 response with paginated data.
func OKPage[T any](w http.ResponseWriter, items []T, total, page, pageSize int) {
	if items == nil {
		items = []T{}
	}
	write(w, http.StatusOK, CodeOK, Page[T]{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, "success")
}

// Error writes an error response with the given code and message.
// The HTTP status is derived from the code.
func Error(w http.ResponseWriter, code Code, message string) {
	if message == "" {
		message = code.String()
	}
	write(w, code.HTTPStatus(), code, nil, message)
}

// ErrorWithStatus writes an error response with an explicit HTTP status,
// overriding the code's default HTTP status.
func ErrorWithStatus(w http.ResponseWriter, status int, code Code, message string) {
	if message == "" {
		message = code.String()
	}
	write(w, status, code, nil, message)
}

func write(w http.ResponseWriter, status int, code Code, data any, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Code: code, Data: data, Message: message})
}
