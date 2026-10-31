package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/aws/smithy-go"
)

type fakeSTS struct {
	calls []sts.AssumeRoleWithWebIdentityInput
	err   error
}

func (f *fakeSTS) AssumeRoleWithWebIdentity(_ context.Context, in *sts.AssumeRoleWithWebIdentityInput, _ ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	profile := aws.ToString(in.RoleSessionName)
	return &sts.AssumeRoleWithWebIdentityOutput{Credentials: &ststypes.Credentials{
		AccessKeyId:     aws.String("AKID-" + profile),
		SecretAccessKey: aws.String("secret-" + profile),
		SessionToken:    aws.String("session-" + profile),
	}}, nil
}

func fakeSVID(_ context.Context, audience string) (string, error) {
	return "svid-for-" + audience, nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func seedBinding(t *testing.T, roleARN string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "role-arn"), []byte(roleARN+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseAWSBindings(t *testing.T) {
	got := parseAWSBindings("/bindings/nosql:nosql, /bindings/s3:object-storage,,broken")
	want := []awsBinding{{"/bindings/nosql", "nosql"}, {"/bindings/s3", "object-storage"}}
	if len(got) != len(want) {
		t.Fatalf("got %d bindings, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("binding %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAWSRefreshWritesOneProfilePerBinding(t *testing.T) {
	fake := &fakeSTS{}
	credsFile := filepath.Join(t.TempDir(), "credentials")
	l := &awsLoop{
		bindings: []awsBinding{
			{seedBinding(t, "arn:aws:iam::000000000000:role/nosql"), "nosql"},
			{seedBinding(t, "arn:aws:iam::000000000000:role/s3"), "object-storage"},
		},
		credsFile: credsFile,
		audience:  "sts.amazonaws.com",
		sts:       fake,
		fetchSVID: fakeSVID,
		logger:    quietLogger(),
	}

	if err := l.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := os.ReadFile(credsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "[nosql]\naws_access_key_id = AKID-nosql\naws_secret_access_key = secret-nosql\naws_session_token = session-nosql\n\n" +
		"[object-storage]\naws_access_key_id = AKID-object-storage\naws_secret_access_key = secret-object-storage\naws_session_token = session-object-storage\n\n"
	if string(got) != want {
		t.Errorf("credentials file:\n%s\nwant:\n%s", got, want)
	}
	if token := aws.ToString(fake.calls[0].WebIdentityToken); token != "svid-for-sts.amazonaws.com" {
		t.Errorf("STS got token %q, want the SVID for the STS audience", token)
	}
}

func TestAWSRefreshKeepsOldFileOnFailure(t *testing.T) {
	credsFile := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(credsFile, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &awsLoop{
		bindings:  []awsBinding{{seedBinding(t, "arn:aws:iam::000000000000:role/nosql"), "nosql"}},
		credsFile: credsFile,
		sts:       &fakeSTS{err: &smithy.GenericAPIError{Code: "InvalidIdentityToken", Message: "token svid-secret is bad"}},
		fetchSVID: fakeSVID,
		logger:    quietLogger(),
	}

	err := l.refresh(context.Background())
	if err == nil {
		t.Fatal("expected an error when STS refuses")
	}
	if strings.Contains(err.Error(), "svid-secret") || strings.Contains(err.Error(), "000000000000") {
		t.Errorf("error leaks the token or the account ID: %v", err)
	}
	if !strings.Contains(err.Error(), "InvalidIdentityToken") {
		t.Errorf("error should name the STS code: %v", err)
	}
	if got, _ := os.ReadFile(credsFile); string(got) != "old" {
		t.Errorf("credentials file changed to %q, want the old one kept", got)
	}
}

func TestAWSRefreshWaitsForBindingFiles(t *testing.T) {
	l := &awsLoop{
		bindings:  []awsBinding{{t.TempDir(), "nosql"}},
		credsFile: filepath.Join(t.TempDir(), "credentials"),
		sts:       &fakeSTS{},
		fetchSVID: fakeSVID,
		logger:    quietLogger(),
	}
	if err := l.refresh(context.Background()); err == nil {
		t.Fatal("expected an error while the binding has no role-arn yet")
	}
}
