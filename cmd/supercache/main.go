// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Command supercache is the Super-Cache server entry point.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/logging"
	"github.com/supercache/supercache/internal/peer"
	"github.com/supercache/supercache/internal/server"
)

// Version is the build label (set via -ldflags "-X main.Version=...").
var Version = "dev"

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "", "path to TOML or YAML configuration file (default: first existing of "+config.DefaultConfigPath+" or "+config.DefaultConfigPathAlt+")")
	flag.Parse()
	if *showVersion {
		fmt.Fprintln(os.Stdout, "supercache version "+Version)
		os.Exit(0)
	}
	slog.Info(fmt.Sprintf("Super-Cache starting, GOMAXPROCS=%d", runtime.NumCPU()))
	path := strings.TrimSpace(*configPath)
	path = config.ResolveConfigPathForLoad(path)
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if sp := strings.TrimSpace(cfg.PeerStateFile); sp != "" {
		if extra, err := peer.LoadPeerStateFile(sp); err == nil && len(extra) > 0 {
			cfg.Peers = config.MergePeerLists(cfg.Peers, extra)
			if err := cfg.Validate(); err != nil {
				fmt.Fprintf(os.Stderr, "peer state merge invalid: %v\n", err)
				os.Exit(1)
			}
		}
	}
	logCleanup, err := logging.Init(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logging init: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if logCleanup != nil {
			logCleanup()
		}
	}()

	srv, err := server.New(cfg)
	if err != nil {
		slog.Error("init server", "err", err)
		os.Exit(1)
	}
	srv.SetBuildVersion(Version)
	srv.SetConfigPath(path)

	slog.Info("supercache starting", "version", Version)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.SetRunCancel(cancel)
	srv.SetOnReload(func(changed []string) {
		needsLog := false
		for _, ch := range changed {
			if ch == "log_level" || ch == "log_output" || ch == "log_format" {
				needsLog = true
				break
			}
		}
		if !needsLog {
			return
		}
		nc, err := logging.Init(srv.CurrentConfig())
		if err != nil {
			slog.Error("logging reinit failed", "err", err)
			return
		}
		if logCleanup != nil {
			logCleanup()
		}
		logCleanup = nc
	})

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("shutdown", "err", err)
		}
		cancel()
	}()

	go func() {
		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		for range hup {
			if _, err := srv.ReloadConfig(); err != nil {
				slog.Error("config reload failed", "err", err)
			}
		}
	}()

	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
