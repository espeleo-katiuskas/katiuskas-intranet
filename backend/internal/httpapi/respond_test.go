package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWriteJSONSetsStatusContentTypeAndBody(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, 201, map[string]string{"hello": "world"})

	if rec.Code != 201 {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json content type, got %q", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["hello"] != "world" {
		t.Errorf("unexpected body: %v", body)
	}
}

func TestWriteJSONWithNilDataWritesNoBody(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, 204, nil)

	if rec.Code != 204 {
		t.Errorf("expected status 204, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected an empty body, got %q", rec.Body.String())
	}
}

func TestWriteErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, 404, "not_found", "user not found")

	if rec.Code != 404 {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "not_found" || body.Error.Message != "user not found" {
		t.Errorf("unexpected error body: %+v", body.Error)
	}
}
