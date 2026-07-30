package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/adminapi"
	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/config"
	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/engine"
	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/sync"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[subscription-sidecar] ")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	var admin sync.AdminClient
	if !cfg.DryRun {
		admin = adminapi.New(cfg.BaseURL, cfg.AdminAPIKey)
	}

	svc, err := sync.NewService(cfg, admin)
	if err != nil {
		log.Fatalf("service: %v", err)
	}
	if v := strings.TrimSpace(os.Getenv("SIDECAR_NODE_ALLOW_CONTAINS")); v != "" {
		svc.AllowContains = splitCSV(v)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var (
		runner     *engine.Runner
		lastHash   string
		engineUp   bool
	)

	runCycle := func() error {
		res, err := svc.RunOnce(ctx)
		if err != nil {
			return err
		}
		log.Printf(
			"sync ok desired=%d created=%d updated=%d unchanged=%d deleted=%d skipped=%d hash=%s dry_run=%v",
			len(res.Desired), res.Created, res.Updated, res.Unchanged, res.Deleted, res.Skipped, shortHash(res.ConfigHash), cfg.DryRun,
		)
		for _, ep := range res.Desired {
			log.Printf("endpoint name=%s node=%q %s://%s:%d", ep.Name, ep.NodeName, ep.Protocol, ep.Host, ep.Port)
		}

		if cfg.SkipEngine || cfg.DryRun {
			return nil
		}

		needStart := !engineUp || res.ConfigHash != lastHash || runner == nil
		if !needStart {
			log.Printf("mihomo unchanged hash=%s", shortHash(res.ConfigHash))
			return nil
		}

		if runner != nil {
			_ = runner.Stop()
			engineUp = false
		}
		runner = &engine.Runner{
			Binary:     cfg.MihomoBinary,
			ConfigPath: cfg.MihomoConfigPath,
			DataDir:    cfg.MihomoDataDir,
		}
		if err := runner.Start(); err != nil {
			return fmt.Errorf("start mihomo: %w (set SIDECAR_SKIP_ENGINE=1 to only write proxies)", err)
		}
		engineUp = true
		lastHash = res.ConfigHash
		log.Printf("mihomo started binary=%s config=%s hash=%s", cfg.MihomoBinary, cfg.MihomoConfigPath, shortHash(res.ConfigHash))
		return nil
	}

	if err := runCycle(); err != nil {
		log.Fatalf("initial sync: %v", err)
	}

	if envTruthy("SIDECAR_ONCE") {
		if runner != nil {
			_ = runner.Stop()
		}
		return
	}

	ticker := time.NewTicker(cfg.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("shutting down")
			if runner != nil {
				_ = runner.Stop()
			}
			return
		case <-ticker.C:
			if err := runCycle(); err != nil {
				log.Printf("sync error: %v", err)
			}
		}
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envTruthy(k string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}
