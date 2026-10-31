// Trades this pod's SPIFFE identity for short-lived cloud credentials.
// Each loop runs only when its env var is set, so a pod can use any mix of them.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	socket := envOr("SPIFFE_SOCKET", "/var/run/secrets/spiffe.io/api.sock")
	fetchSVID := spiffeFetcher("unix://" + socket)
	entraAudience := envOr("ENTRA_AUDIENCE", "api://AzureADTokenExchange")

	var loops []func(context.Context) error

	if v := os.Getenv("AWS_BINDINGS"); v != "" {
		loops = append(loops, newAWSLoop(v, logger, fetchSVID).run)
	}

	if path := os.Getenv("ENTRA_FEDERATED_TOKEN_FILE"); path != "" {
		l := entraLoop{
			path:      path,
			audience:  entraAudience,
			interval:  envSeconds("ENTRA_REFRESH_INTERVAL", 240),
			fetchSVID: fetchSVID,
			logger:    logger,
		}
		loops = append(loops, l.run)
	}

	if name := os.Getenv("TOKEN_SECRET_NAME"); name != "" {
		l, err := newTokenSecretLoop(name, entraAudience, logger, fetchSVID)
		if err != nil {
			logger.Error("cannot start the token secret loop", "error", err)
			os.Exit(1)
		}
		loops = append(loops, l.run)
	}

	if v := os.Getenv("SECRET_BINDINGS"); v != "" {
		l, err := newSecretsLoop(v, logger, fetchSVID)
		if err != nil {
			logger.Error("cannot read secret bindings", "error", err)
			os.Exit(1)
		}
		loops = append(loops, l.run)
	}

	if len(loops) == 0 {
		logger.Info("no bindings configured, nothing to do")
		<-ctx.Done()
		return
	}

	waitForSocket(ctx, socket, logger)

	exited := make(chan struct{}, len(loops))
	for _, loop := range loops {
		go func() {
			_ = loop(ctx)
			exited <- struct{}{}
		}()
	}

	// Every loop retries forever, so one that returns early hit something unexpected.
	// Exiting lets the kubelet restart the container.
	// Staying up would let that loop's credentials expire behind a healthy-looking pod.
	select {
	case <-ctx.Done():
	case <-exited:
		if ctx.Err() == nil {
			logger.Error("a refresh loop exited, stopping container")
			os.Exit(1)
		}
	}
}

func waitForSocket(ctx context.Context, socket string, logger *slog.Logger) {
	for {
		if info, err := os.Stat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		logger.Info("waiting for SPIFFE socket", "path", socket)
		if !sleep(ctx, 2*time.Second) {
			return
		}
	}
}

// sleep waits for d, and returns false if the context ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envSeconds(name string, fallback int) time.Duration {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(fallback) * time.Second
}
