package main

import (
	"context"
	"log/slog"
	"time"
)

// entraLoop keeps a fresh JWT-SVID in a file. The app's Azure SDK trades it with Entra
// itself, the same contract Azure Kubernetes Service uses. This loop never calls Entra.
type entraLoop struct {
	path     string
	audience string
	// Under SPIRE's 5-minute JWT-SVID lifetime.
	interval  time.Duration
	fetchSVID svidFetcher
	logger    *slog.Logger
}

func (l entraLoop) run(ctx context.Context) error {
	for {
		wait := l.interval
		if err := l.refresh(ctx); err != nil {
			l.logger.Warn("Entra token file not refreshed", "error", err)
			wait = 10 * time.Second
		}
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
	}
}

func (l entraLoop) refresh(ctx context.Context) error {
	token, err := l.fetchSVID(ctx, l.audience)
	if err != nil {
		return err
	}
	if err := writeFile(l.path, token, 0o600); err != nil {
		return err
	}
	l.logger.Info("Entra token file refreshed", "path", l.path)
	return nil
}
