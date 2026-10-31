package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

const (
	// STS sessions last 1 hour.
	awsRefreshInterval = 50 * time.Minute
	// A credentials file already written stays valid for up to an hour, so back off.
	awsRetryInterval = 30 * time.Second
	// A new IAM role is often refused for a few seconds, and the app waits on the file.
	awsFirstRetryInterval = 2 * time.Second
)

// awsBinding is one AWS resource binding: a folder holding role-arn, and a profile name.
type awsBinding struct {
	dir     string
	profile string
}

// assumeRoleAPI is the one STS call this loop makes, so tests can fake it.
type assumeRoleAPI interface {
	AssumeRoleWithWebIdentity(ctx context.Context, in *sts.AssumeRoleWithWebIdentityInput, opts ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

type awsLoop struct {
	bindings  []awsBinding
	credsFile string
	audience  string
	sts       assumeRoleAPI
	fetchSVID svidFetcher
	logger    *slog.Logger
}

func newAWSLoop(pairs string, logger *slog.Logger, fetchSVID svidFetcher) *awsLoop {
	// Regional, not the global endpoint. The mesh egress allowlist only permits this host.
	endpoint := envOr("STS_ENDPOINT", "https://sts.us-east-1.amazonaws.com")
	// AssumeRoleWithWebIdentity is unsigned. The token is the proof, so no credentials are needed.
	client := sts.New(sts.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(endpoint),
		Credentials:  aws.AnonymousCredentials{},
	})
	return &awsLoop{
		bindings:  parseAWSBindings(pairs),
		credsFile: os.Getenv("CREDS_FILE"),
		// Must match a client ID on the AWS identity provider, or STS refuses the token.
		audience:  envOr("STS_AUDIENCE", "sts.amazonaws.com"),
		sts:       client,
		fetchSVID: fetchSVID,
		logger:    logger,
	}
}

// parseAWSBindings reads AWS_BINDINGS, such as "/bindings/nosql:nosql,/bindings/s3:s3".
func parseAWSBindings(pairs string) []awsBinding {
	var bindings []awsBinding
	for _, pair := range strings.Split(pairs, ",") {
		dir, profile, ok := strings.Cut(strings.TrimSpace(pair), ":")
		if !ok || dir == "" || profile == "" {
			continue
		}
		bindings = append(bindings, awsBinding{dir: dir, profile: profile})
	}
	return bindings
}

func (l *awsLoop) run(ctx context.Context) error {
	for {
		wait := awsRefreshInterval
		if err := l.refresh(ctx); err != nil {
			l.logger.Warn("AWS credentials not refreshed, keeping the previous file", "error", err)
			wait = awsFirstRetryInterval
			if info, statErr := os.Stat(l.credsFile); statErr == nil && info.Size() > 0 {
				wait = awsRetryInterval
			}
		}
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
	}
}

// refresh trades one JWT-SVID for STS credentials per binding.
// It writes the file only when every binding succeeds, so the app never sees a partial set.
func (l *awsLoop) refresh(ctx context.Context) error {
	token, err := l.fetchSVID(ctx, l.audience)
	if err != nil {
		return err
	}

	var file strings.Builder
	for _, b := range l.bindings {
		roleARN, err := readFile(b.dir, "role-arn")
		if err != nil {
			return fmt.Errorf("binding %s not ready: %w", b.profile, err)
		}

		out, err := l.sts.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
			RoleArn:          aws.String(roleARN),
			RoleSessionName:  aws.String(b.profile),
			WebIdentityToken: aws.String(token),
			DurationSeconds:  aws.Int32(3600),
		})
		if err != nil {
			// The role name only. The full ARN holds the account ID, and these logs ship to Loki.
			return fmt.Errorf("STS refused profile %s (role %s): %s", b.profile, path.Base(roleARN), errorCode(err))
		}
		if out.Credentials == nil {
			return fmt.Errorf("STS returned no credentials for profile %s", b.profile)
		}
		writeProfile(&file, b.profile, out)
	}

	if err := writeFile(l.credsFile, file.String(), 0o600); err != nil {
		return err
	}
	l.logger.Info("AWS credentials file updated", "path", l.credsFile)
	return nil
}

func writeProfile(file *strings.Builder, profile string, out *sts.AssumeRoleWithWebIdentityOutput) {
	fmt.Fprintf(file, "[%s]\n", profile)
	fmt.Fprintf(file, "aws_access_key_id = %s\n", aws.ToString(out.Credentials.AccessKeyId))
	fmt.Fprintf(file, "aws_secret_access_key = %s\n", aws.ToString(out.Credentials.SecretAccessKey))
	fmt.Fprintf(file, "aws_session_token = %s\n\n", aws.ToString(out.Credentials.SessionToken))
}

// errorCode keeps only the error code. The full message can echo the token back.
func errorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return "request failed"
}
