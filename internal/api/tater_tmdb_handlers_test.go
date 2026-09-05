package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestTaterTMDBConnectionUsesEnteredOrSavedCredential(t *testing.T) {
	oldBaseURL := taterTMDBBaseURL
	oldClient := taterTMDBHTTPClient
	t.Cleanup(func() {
		taterTMDBBaseURL = oldBaseURL
		taterTMDBHTTPClient = oldClient
	})

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/configuration" {
			http.NotFound(response, request)
			return
		}
		if request.URL.Query().Get("api_key") != "entered-key" && request.Header.Get("Authorization") != "Bearer saved.token.with.dots" {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"images":{}}`))
	}))
	defer server.Close()
	taterTMDBBaseURL = server.URL
	taterTMDBHTTPClient = server.Client()

	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{TMDBAPIKey: "saved.token.with.dots"}}
	if err := testTaterTMDBConnection(context.Background(), cfg, "entered-key"); err != nil {
		t.Fatalf("entered API key was rejected: %v", err)
	}
	if err := testTaterTMDBConnection(context.Background(), cfg, ""); err != nil {
		t.Fatalf("saved read token was rejected: %v", err)
	}
	if err := testTaterTMDBConnection(context.Background(), &config.Config{}, ""); err == nil {
		t.Fatal("expected an empty credential to be rejected")
	}
}

func TestTaterTMDBConnectionReportsRejectedCredential(t *testing.T) {
	oldBaseURL := taterTMDBBaseURL
	oldClient := taterTMDBHTTPClient
	t.Cleanup(func() {
		taterTMDBBaseURL = oldBaseURL
		taterTMDBHTTPClient = oldClient
	})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	taterTMDBBaseURL = server.URL
	taterTMDBHTTPClient = server.Client()

	err := testTaterTMDBConnection(context.Background(), &config.Config{}, "bad-key")
	if err == nil || err.Error() != "TMDB rejected this key or token" {
		t.Fatalf("unexpected rejected credential error: %v", err)
	}
}
