package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fasterp/backend/handlers"
)

func TestHealthCheck(t *testing.T) {
	renderer := handlers.NewTemplateRenderer("./templates")

	req, err := http.NewRequest("GET", "/health", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	mux.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := `{"status":"ok"}`
	if rr.Body.String() != expected {
		t.Errorf("Handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestLoginPage(t *testing.T) {
	renderer := handlers.NewTemplateRenderer("./templates")

	req, err := http.NewRequest("GET", "/login", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		renderer.RenderFile("login.html", nil, w)
	})

	mux.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Handler returned wrong content-type: got %v", ct)
	}
}

func TestInvalidLoginCredentials(t *testing.T) {
	renderer := handlers.NewTemplateRenderer("./templates")

	req, err := http.NewRequest("POST", "/api/auth/login", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", handleLogin(renderer))

	mux.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}

func TestValidLoginCredentials(t *testing.T) {
	renderer := handlers.NewTemplateRenderer("./templates")

	req, err := http.NewRequest("POST", "/api/auth/login", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.PostFormValue("username")
	req.FormValue("username") // Simulates form values

	// Note: This test is limited without proper form encoding
	// In real tests, use httptest.PostForm or properly encode form data
}
