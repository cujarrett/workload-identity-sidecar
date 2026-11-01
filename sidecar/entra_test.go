package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
