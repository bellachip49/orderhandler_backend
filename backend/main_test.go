package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()

	db, err := openDatabase(":memory:")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	return newHandler(db)
}

func TestHealthEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	newTestHandler(t).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status body field = %q, want ok", body["status"])
	}
}

func TestUnknownRouteReturnsJSONNotFound(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	response := httptest.NewRecorder()

	newTestHandler(t).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "not found" {
		t.Fatalf("error body field = %q, want not found", body["error"])
	}
}

func TestOrdersEndpointReturnsEmptyCollection(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/orders", nil)
	response := httptest.NewRecorder()

	newTestHandler(t).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}

	var orders []json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&orders); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(orders) != 0 {
		t.Fatalf("orders length = %d, want 0", len(orders))
	}
}

func TestCreateOrderAndListOrders(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{
        "items": [
            {"name": "Milk Tea", "price": 4.5, "quantity": 2},
            {"name": "Dumplings", "price": 6, "quantity": 1}
        ],
        "total": 15
    }`)
	request := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want %d", response.Code, http.StatusCreated)
	}
	var created Order
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("decode created order: %v", err)
	}
	if created.ID != 1 {
		t.Fatalf("created order ID = %d, want 1", created.ID)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("created_at must be assigned")
	}
	if len(created.Items) != 2 || created.Total != 15 {
		t.Fatalf("created order = %+v, want two items totaling 15", created)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/orders", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)

	var orders []Order
	if err := json.NewDecoder(listResponse.Body).Decode(&orders); err != nil {
		t.Fatalf("decode order list: %v", err)
	}
	if len(orders) != 1 || orders[0].ID != 1 || orders[0].CreatedAt.IsZero() || orders[0].Items[0].Name != "Milk Tea" {
		t.Fatalf("orders = %+v, want the created Milk Tea order", orders)
	}
}

func TestCreateOrderAssignsIncrementingIDs(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"items": [{"name": "Fish balls", "price": 5, "quantity": 1}], "total": 5}`)

	for wantID := int64(1); wantID <= 2; wantID++ {
		request := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewReader(body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		var created Order
		if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
			t.Fatalf("decode created order: %v", err)
		}
		if created.ID != wantID {
			t.Fatalf("created order ID = %d, want %d", created.ID, wantID)
		}
	}
}

func TestCreateOrderRejectsInvalidPayload(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"items": [], "total": 0}`))
	response := httptest.NewRecorder()

	newTestHandler(t).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestOrdersEndpointRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/orders", nil)
	response := httptest.NewRecorder()

	newTestHandler(t).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	if allow := response.Header().Get("Allow"); allow != "GET, POST" {
		t.Fatalf("Allow = %q, want GET, POST", allow)
	}
}
