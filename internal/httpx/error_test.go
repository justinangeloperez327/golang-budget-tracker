package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("X-Request-ID", "test-id")
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusForbidden, "forbidden", "Access denied")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	var v struct {
		Error struct {
			Code string `json:"code"`
			Message string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Error.Code != "forbidden" || v.Error.Message != "Access denied" || v.Error.RequestID != "test-id" {
		t.Fatalf("response = %+v", v)
	}
}
