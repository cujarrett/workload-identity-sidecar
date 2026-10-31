package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Entra access tokens last 60 to 90 minutes.
	tokenRefreshInterval = 45 * time.Minute
	tokenRetryInterval   = 30 * time.Second
	serviceAccountDir    = "/var/run/secrets/kubernetes.io/serviceaccount"
)

// tokenSecretLoop keeps an Entra access token in a Kubernetes Secret.
// It serves controllers that read their credential only from a Secret, such as
// Temporal's Worker Controller.
type tokenSecretLoop struct {
	secretName string
	secretKey  string
	scope      string
	tenantID   string
	clientID   string
	audience   string
	interval   time.Duration
	saDir      string
	namespace  string

	entraBaseURL string
	apiServerURL string
	entra        *http.Client
	apiServer    *http.Client

	fetchSVID svidFetcher
	logger    *slog.Logger
}

func newTokenSecretLoop(secretName, audience string, logger *slog.Logger, fetchSVID svidFetcher) (*tokenSecretLoop, error) {
	l := &tokenSecretLoop{
		secretName:   secretName,
		secretKey:    envOr("TOKEN_SECRET_KEY", "token"),
		scope:        os.Getenv("TOKEN_SCOPE"),
		tenantID:     os.Getenv("AZURE_TENANT_ID"),
		clientID:     os.Getenv("AZURE_CLIENT_ID"),
		audience:     audience,
		interval:     envSeconds("TOKEN_REFRESH_INTERVAL", int(tokenRefreshInterval.Seconds())),
		saDir:        serviceAccountDir,
		entraBaseURL: "https://login.microsoftonline.com",
		apiServerURL: "https://kubernetes.default.svc",
		entra:        &http.Client{Timeout: 30 * time.Second},
		fetchSVID:    fetchSVID,
		logger:       logger,
	}
	if l.scope == "" || l.tenantID == "" || l.clientID == "" {
		return nil, errors.New("TOKEN_SECRET_NAME needs TOKEN_SCOPE, AZURE_TENANT_ID and AZURE_CLIENT_ID")
	}
	return l, l.loadServiceAccount()
}

// loadServiceAccount reads the pod's namespace and the API server's CA.
// The token itself is read on every write, because kubelet rotates it.
func (l *tokenSecretLoop) loadServiceAccount() error {
	ns, err := readFile(l.saDir, "namespace")
	if err != nil {
		return err
	}
	l.namespace = ns

	ca, err := os.ReadFile(filepath.Join(l.saDir, "ca.crt"))
	if err != nil {
		return fmt.Errorf("reading the API server CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return errors.New("the API server CA holds no certificate")
	}
	l.apiServer = &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	}
	return nil
}

func (l *tokenSecretLoop) run(ctx context.Context) error {
	for {
		wait := l.interval
		if err := l.refresh(ctx); err != nil {
			l.logger.Warn("token Secret not refreshed", "secret", l.secretName, "error", err)
			wait = tokenRetryInterval
		}
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
	}
}

func (l *tokenSecretLoop) refresh(ctx context.Context) error {
	assertion, err := l.fetchSVID(ctx, l.audience)
	if err != nil {
		return err
	}
	token, err := l.exchange(ctx, assertion)
	if err != nil {
		return err
	}
	if err := l.writeSecret(ctx, token); err != nil {
		return err
	}
	l.logger.Info("Entra access token written to Secret", "secret", l.secretName)
	return nil
}

// exchange trades the JWT-SVID with Entra for an access token, through the federated
// credential on the app registration. The SVID stands in for a client secret.
func (l *tokenSecretLoop) exchange(ctx context.Context, assertion string) (string, error) {
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_id":             {l.clientID},
		"scope":                 {l.scope},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
	}
	tokenURL := fmt.Sprintf("%s/%s/oauth2/v2.0/token", l.entraBaseURL, url.PathEscape(l.tenantID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := l.entra.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling Entra: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("reading Entra's reply (status %d): %w", resp.StatusCode, err)
	}
	if body.AccessToken == "" {
		// Only the error code. The description can echo the assertion back.
		return "", fmt.Errorf("token request refused by Entra: %s", body.Error)
	}
	return body.AccessToken, nil
}

// writeSecret patches the token into the Secret. The Secret must already exist,
// because the pod can only be granted patch on one named Secret, not create.
func (l *tokenSecretLoop) writeSecret(ctx context.Context, token string) error {
	patch, err := json.Marshal(map[string]map[string]string{"stringData": {l.secretKey: token}})
	if err != nil {
		return err
	}
	saToken, err := readFile(l.saDir, "token")
	if err != nil {
		return err
	}

	secretURL := fmt.Sprintf("%s/api/v1/namespaces/%s/secrets/%s",
		l.apiServerURL, url.PathEscape(l.namespace), url.PathEscape(l.secretName))
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, secretURL, bytes.NewReader(patch))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+saToken)
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, err := l.apiServer.Do(req)
	if err != nil {
		return fmt.Errorf("calling the API server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the API server refused the Secret write: status %d", resp.StatusCode)
	}
	return nil
}
