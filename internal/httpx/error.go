// Package httpx defines consistent, safe JSON error responses for HTTP endpoints.
package httpx

import (
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Error errorDetails `json:"error"`
}

type errorDetails struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Error: errorDetails{
			Code:      code,
			Message:   message,
			RequestID: w.Header().Get("X-Request-ID"),
		},
	})
}
