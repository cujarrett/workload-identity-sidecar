package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	// A rotated value lands without a restart, and nobody waits a working day for one.
	secretRefreshInterval = 15 * time.Minute
	// A failed fetch keeps the files already written, so retry sooner without hammering AWS.
	secretRetryInterval = 30 * time.Second
)

// secretBinding is one ManagedSecret mounted into this pod.
// Dir is a read-only Secret volume, so the value is written to OutDir instead.
// Every property the value carries is written, because a composition cannot read
// another XR's spec to know which ones the app wants.
type secretBinding struct {
	Dir      string
	OutDir   string
	RoleARN  string
	SecretID string
	Region   string
}

type secretsLoop struct {
	bindings  []secretBinding
	audience  string
	fetchSVID svidFetcher
	logger    *slog.Logger
}

func newSecretsLoop(pairs string, logger *slog.Logger, fetchSVID svidFetcher) (*secretsLoop, error) {
	bindings, err := loadBindings(strings.Split(pairs, ","))
	if err != nil {
		return nil, err
	}
	return &secretsLoop{
		bindings:  bindings,
		audience:  envOr("STS_AUDIENCE", "sts.amazonaws.com"),
		fetchSVID: fetchSVID,
		logger:    logger,
	}, nil
}

func (l *secretsLoop) run(ctx context.Context) error {
	for {
		wait := secretRefreshInterval
		for _, b := range l.bindings {
			written, err := l.sync(ctx, b)
			if err != nil {
				// Never fatal. Files already written stay valid.
				// The startup probe gating the app surfaces a value that never arrived.
				l.logger.Warn("sync failed, keeping existing files",
					"binding", b.Dir, "secret", b.SecretID, "error", err)
				wait = secretRetryInterval
				continue
			}
			l.logger.Info("secret written", "binding", b.Dir, "keys", written)
		}
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
	}
}

// sync fetches one secret and writes a file per property, returning how many.
// The ready file is written last, so the app is never released on a half-written set.
func (l *secretsLoop) sync(ctx context.Context, b secretBinding) (int, error) {
	value, err := l.fetch(ctx, b)
	if err != nil {
		return 0, err
	}

	props, err := parse(value)
	if err != nil {
		return 0, err
	}

	for key, val := range props {
		if err := writeFile(filepath.Join(b.OutDir, key), val, 0o400); err != nil {
			return 0, err
		}
	}
	return len(props), writeFile(filepath.Join(b.OutDir, "ready"), "true", 0o400)
}

// parse turns a secret's JSON value into properties.
func parse(value string) (map[string]string, error) {
	var props map[string]string
	if err := json.Unmarshal([]byte(value), &props); err != nil {
		return nil, fmt.Errorf("secret value is not a flat JSON object: %w", err)
	}
	if len(props) == 0 {
		return nil, errors.New("waiting for value - secret holds no properties")
	}
	return props, nil
}

// fetch trades this pod's SVID for credentials scoped to one secret, then reads it.
// GetSecretValue is SigV4-signed, so this call needs the AWS SDK.
// The SDK refreshes the STS credentials itself.
func (l *secretsLoop) fetch(ctx context.Context, b secretBinding) (string, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(b.Region))
	if err != nil {
		return "", fmt.Errorf("loading AWS config: %w", err)
	}

	provider := stscreds.NewWebIdentityRoleProvider(
		sts.NewFromConfig(cfg),
		b.RoleARN,
		svidRetriever{audience: l.audience, fetchSVID: l.fetchSVID},
		func(o *stscreds.WebIdentityRoleOptions) {
			// Names the session after the secret, so CloudTrail says which binding made the call.
			o.RoleSessionName = "managed-secret-" + filepath.Base(b.OutDir)
		},
	)
	cfg.Credentials = aws.NewCredentialsCache(provider)

	out, err := secretsmanager.NewFromConfig(cfg).GetSecretValue(ctx,
		&secretsmanager.GetSecretValueInput{SecretId: aws.String(b.SecretID)})
	// A ManagedSecret is created with no version, so NotFound until the owner sets one.
	var notFound *smtypes.ResourceNotFoundException
	if errors.As(err, &notFound) {
		return "", errors.New("waiting for value - owner has not set one yet")
	}
	if err != nil {
		return "", fmt.Errorf("getting secret value: %w", err)
	}
	if out.SecretString == nil {
		return "", errors.New("secret holds binary data, which the platform does not deliver")
	}
	return *out.SecretString, nil
}

// svidRetriever hands the AWS SDK a fresh JWT-SVID for each exchange.
type svidRetriever struct {
	audience  string
	fetchSVID svidFetcher
}

func (s svidRetriever) GetIdentityToken() ([]byte, error) {
	token, err := s.fetchSVID(context.Background(), s.audience)
	if err != nil {
		return nil, err
	}
	return []byte(token), nil
}

// loadBindings reads SECRET_BINDINGS entries, each "bindingDir:outputDir".
func loadBindings(pairs []string) ([]secretBinding, error) {
	var bindings []secretBinding
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
		bindings = append(bindings, secretBinding{
			Dir:      dir,
			OutDir:   outDir,
			RoleARN:  role,
			SecretID: secretID,
			Region:   region,
		})
	}
	return bindings, nil
}
