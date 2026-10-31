package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestTokenLoop(t *testing.T, entra, apiServer *httptest.Server) *tokenSecretLoop {
	t.Helper()
	saDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(saDir, "token"), []byte("sa-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &tokenSecretLoop{
		secretName:   "worker-controller-token",
		secretKey:    "token",
		scope:        "api://tenant/platform-temporal/.default",
		tenantID:     "tenant",
		clientID:     "client",
		audience:     "api://AzureADTokenExchange",
		saDir:        saDir,
		namespace:    "foo",
		entraBaseURL: entra.URL,
		apiServerURL: apiServer.URL,
		entra:        entra.Client(),
		apiServer:    apiServer.Client(),
		fetchSVID:    fakeSVID,
		logger:       quietLogger(),
	}
}

func TestTokenSecretRefresh(t *testing.T) {
	entra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tenant/oauth2/v2.0/token" {
			t.Errorf("Entra path %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.PostForm.Get("client_assertion"); got != "svid-for-api://AzureADTokenExchange" {
			t.Errorf("client_assertion = %q, want the SVID for the Entra audience", got)
		}
		if got := r.PostForm.Get("scope"); got != "api://tenant/platform-temporal/.default" {
			t.Errorf("scope = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "entra-access-token", "expires_in": 3599})
	}))
	defer entra.Close()

	var patched map[string]map[string]string
	apiServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/namespaces/foo/secrets/worker-controller-token" {
			t.Errorf("API server got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sa-token" {
			t.Errorf("Authorization = %q, want the service account token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/merge-patch+json" {
			t.Errorf("Content-Type = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &patched); err != nil {
			t.Fatal(err)
		}
	}))
	defer apiServer.Close()

	l := newTestTokenLoop(t, entra, apiServer)
	if err := l.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := patched["stringData"]["token"]; got != "entra-access-token" {
		t.Errorf("Secret got token %q, want the Entra access token", got)
	}
}

func TestTokenSecretEntraRefusal(t *testing.T) {
	entra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_client",
			"error_description": "assertion svid-for-api://AzureADTokenExchange was refused",
		})
	}))
	defer entra.Close()
	apiServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the Secret must not be written when Entra refuses")
	}))
	defer apiServer.Close()

	err := newTestTokenLoop(t, entra, apiServer).refresh(context.Background())
	if err == nil {
		t.Fatal("expected an error when Entra refuses")
	}
	if !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("error should name Entra's code: %v", err)
	}
	if strings.Contains(err.Error(), "svid-for") {
		t.Errorf("error leaks the assertion: %v", err)
	}
}

func TestTokenSecretAPIServerRefusal(t *testing.T) {
	entra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "entra-access-token"})
	}))
	defer entra.Close()
	apiServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer apiServer.Close()

	err := newTestTokenLoop(t, entra, apiServer).refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected a 403 error, got %v", err)
	}
}

func TestEntraLoopWritesSVID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entra-identity", "token")
	l := entraLoop{path: path, audience: "api://AzureADTokenExchange", fetchSVID: fakeSVID, logger: quietLogger()}
	if err := l.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "svid-for-api://AzureADTokenExchange" {
		t.Errorf("token file = %q", got)
	}
}
