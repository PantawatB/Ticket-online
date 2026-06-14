package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminMiddlewareRejectsUserRole(t *testing.T) {
	app := &App{cfg: Config{JWTSecret: "test-secret"}}
	token, err := signJWT(&User{ID: "user-1", Email: "user@example.com", Role: roleUser}, app.cfg.JWTSecret)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/bookings", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler := app.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), roleAdmin)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for user role, got %d", rr.Code)
	}
}

func TestAdminMiddlewareAllowsAdminRole(t *testing.T) {
	app := &App{cfg: Config{JWTSecret: "test-secret"}}
	token, err := signJWT(&User{ID: "admin-1", Email: "admin@example.com", Role: roleAdmin}, app.cfg.JWTSecret)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/bookings", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler := app.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), roleAdmin)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin role, got %d", rr.Code)
	}
}

func TestBuildBookingEmailIncludesBookingDetails(t *testing.T) {
	booking := Booking{
		ID:         "booking-1",
		UserEmail:  "buyer@example.com",
		EventTitle: "Dekmantel Festival 2019 - Wednesday",
		SeatID:     "A1",
		Status:     "CONFIRMED",
	}

	subject, body := buildBookingEmail(booking)

	for _, want := range []string{
		"Ticket Online booking confirmed",
		"Dekmantel Festival 2019 - Wednesday",
		"A1",
		"CONFIRMED",
		"booking-1",
	} {
		if !strings.Contains(subject+" "+body, want) {
			t.Fatalf("expected email content to contain %q, got subject=%q body=%q", want, subject, body)
		}
	}
}
