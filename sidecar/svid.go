package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// svidFetcher returns a fresh JWT-SVID for one audience.
// Loops take it as a field, so tests can hand them a fake token.
type svidFetcher func(ctx context.Context, audience string) (string, error)

// spiffeFetcher asks the local SPIRE agent for a JWT-SVID over the Workload API socket.
// Nothing leaves the cluster here.
func spiffeFetcher(addr string) svidFetcher {
	return func(ctx context.Context, audience string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		svid, err := workloadapi.FetchJWTSVID(ctx, jwtsvid.Params{Audience: audience}, workloadapi.WithAddr(addr))
		if err != nil {
			return "", fmt.Errorf("fetching JWT-SVID: %w", err)
		}
		return svid.Marshal(), nil
	}
}

// writeFile renames into place, so an app never reads a half-written file.
// A temp file left by a crash may be read-only, so it is removed first.
func writeFile(path, content string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(content), perm); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s: %w", tmp, err)
	}
	return nil
}

func readFile(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filepath.Join(dir, name), err)
	}
	return strings.TrimSpace(string(b)), nil
}
