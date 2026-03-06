package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetUser_ValidID(t *testing.T) {
	req := httptest.NewRequest("GET", "/user?id=123", nil)
	w := httptest.NewRecorder()

	GetUser(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp UserResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Name != "Alice" {
		t.Errorf("expected Alice, got %q", resp.Name)
	}
}

func TestGetUser_InvalidID_Zero(t *testing.T) {
	req := httptest.NewRequest("GET", "/user?id=0", nil)
	w := httptest.NewRecorder()

	GetUser(w, req)

	// AC-2: must return 400
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for id=0, got %d", w.Code)
	}
}

func TestGetUser_NotFound(t *testing.T) {
	req := httptest.NewRequest("GET", "/user?id=999", nil)
	w := httptest.NewRecorder()

	GetUser(w, req)

	// AC-3: must return 404
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for id=999, got %d", w.Code)
	}
}
