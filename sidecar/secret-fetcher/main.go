// Fetches a ManagedSecret's value and writes each property as a file. A secret is a
// value rather than a service, so it arrives as a file like every other binding, and
// no app carries AWS SDK code to read one.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	// Matches the placeholder the ManagedSecret composition writes at creation, because
	// CreateSecret refuses a secret with no value at all. Seeing it means the owner has
	// not set a real value yet.
	placeholderKey = "__unset__"

	// Long enough that a rotated value lands without a restart, short enough that nobody
	// waits a working day for one. The STS credentials behind it last an hour.
	refreshInterval = 15 * time.Minute

	// A failed fetch keeps whatever files are already written, so retry sooner than the
	// steady-state interval without hammering AWS.
	retryInterval = 30 * time.Second
)

// binding is one ManagedSecret mounted into this pod. Which properties it should hold is
// absent because a composition cannot read another XR's spec, so every property the value
// carries is written.
//
// Dir and OutDir differ because Dir is a Secret volume, which is read-only.
type binding struct {
	Dir      string
	OutDir   string
	RoleARN  string
	SecretID string
	Region   string
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dirs := strings.Split(os.Getenv("SECRET_BINDINGS"), ",")
	bindings, err := loadBindings(dirs)
	if err != nil {
		logger.Error("cannot read bindings", "error", err)
		os.Exit(1)
	}
	if len(bindings) == 0 {
		logger.Info("no secret bindings configured, nothing to do")
		<-ctx.Done()
		return
	}

	audience := envOr("STS_AUDIENCE", "sts.amazonaws.com")
	socket := envOr("SPIFFE_SOCKET", "/var/run/secrets/spiffe.io/api.sock")

	for {
		interval := refreshInterval
		for _, b := range bindings {
			written, err := sync(ctx, b, audience, socket)
			if err != nil {
				// Never fatal. Files already written stay valid, and the init container
				// gating the app is what surfaces a value that has never arrived.
				logger.Warn("sync failed, keeping existing files",
					"binding", b.Dir, "secret", b.SecretID, "error", err)
				interval = retryInterval
				continue
			}
			logger.Info("secret written", "binding", b.Dir, "keys", written)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// sync fetches one secret and writes a file per property, returning how many. The ready
// sentinel is written last, so the init container gating the app never releases it on a
// half-written set.
func sync(ctx context.Context, b binding, audience, socket string) (int, error) {
	value, err := fetch(ctx, b, audience, socket)
	if err != nil {
		return 0, err
	}

	props, err := parse(value)
	if err != nil {
		return 0, err
	}

	for key, val := range props {
		if err := writeFile(filepath.Join(b.OutDir, key), val); err != nil {
			return 0, err
		}
	}

	return len(props), writeFile(filepath.Join(b.OutDir, "ready"), "true")
}

// parse turns a secret's JSON value into properties, refusing a value the owner has not
// actually set yet so the app blocks rather than starting with a placeholder.
func parse(value string) (map[string]string, error) {
	var props map[string]string
	if err := json.Unmarshal([]byte(value), &props); err != nil {
		return nil, fmt.Errorf("secret value is not a flat JSON object: %w", err)
	}

	if _, unset := props[placeholderKey]; unset {
		return nil, errors.New("waiting for value - owner has not set one yet")
	}
	if len(props) == 0 {
		return nil, errors.New("waiting for value - secret holds no properties")
	}
	return props, nil
}

// fetch trades this pod's SVID for credentials scoped to one secret, then reads it.
// The SDK refreshes the STS credentials on its own, so this holds no expiry logic.
func fetch(ctx context.Context, b binding, audience, socket string) (string, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(b.Region))
	if err != nil {
		return "", fmt.Errorf("loading AWS config: %w", err)
	}

	provider := stscreds.NewWebIdentityRoleProvider(
		sts.NewFromConfig(cfg),
		b.RoleARN,
		svidRetriever{audience: audience, socket: socket},
		func(o *stscreds.WebIdentityRoleOptions) {
			// Names the session after the secret, so CloudTrail says which binding
			// made the call rather than just which pod.
			o.RoleSessionName = "managed-secret-" + filepath.Base(b.OutDir)
		},
	)
	cfg.Credentials = aws.NewCredentialsCache(provider)

	out, err := secretsmanager.NewFromConfig(cfg).GetSecretValue(ctx,
		&secretsmanager.GetSecretValueInput{SecretId: aws.String(b.SecretID)})
	if err != nil {
		return "", fmt.Errorf("getting secret value: %w", err)
	}
	if out.SecretString == nil {
		return "", errors.New("secret holds binary data, which the platform does not deliver")
	}
	return *out.SecretString, nil
}

// svidRetriever hands the SDK a fresh JWT-SVID per exchange, shelling out to the
// spire-agent binary this image already carries for the shell loops.
type svidRetriever struct {
	audience string
	socket   string
}

func (s svidRetriever) GetIdentityToken() ([]byte, error) {
	out, err := exec.Command("spire-agent", "api", "fetch", "jwt",
		"-audience", s.audience, "-socketPath", s.socket).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching JWT-SVID: %w", err)
	}

	// The token is the second line, indented under a token(...) header.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return nil, errors.New("spire-agent returned no token")
	}
	token := strings.TrimSpace(lines[1])
	if token == "" {
		return nil, errors.New("spire-agent returned an empty token")
	}
	return []byte(token), nil
}

// loadBindings reads SECRET_BINDINGS entries, each "bindingDir:outputDir".
func loadBindings(pairs []string) ([]binding, error) {
	var bindings []binding
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		dir, outDir, ok := strings.Cut(pair, ":")
		if !ok {
			return nil, fmt.Errorf("binding %q is not bindingDir:outputDir", pair)
		}

		role, err := readFile(dir, "role-arn")
		if err != nil {
			return nil, err
		}
		secretID, err := readFile(dir, "secret-id")
		if err != nil {
			return nil, err
		}
		region, err := readFile(dir, "region")
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding{
			Dir:      dir,
			OutDir:   outDir,
			RoleARN:  role,
			SecretID: secretID,
			Region:   region,
		})
	}
	return bindings, nil
}

func readFile(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filepath.Join(dir, name), err)
	}
	return strings.TrimSpace(string(b)), nil
}

// writeFile renames into place so an app never reads a half-written value.
func writeFile(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o400); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s: %w", tmp, err)
	}
	return nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
