// Package httpapi holds transport-level concerns shared by every resource:
// the router, response helpers and request middleware.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("encode response", "error", err)
	}
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: message}})
}
