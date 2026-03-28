// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Super-Cache server entry point: config loading, signal handling, graceful shutdown.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/logging"
	"github.com/supercache/supercache/internal/peer"
	"github.com/supercache/supercache/internal/server"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	var (
		configPath  string
		showVersion bool
	)
	flag.StringVar(&configPath, "config", "", "path to configuration file (TOML or YAML)")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println("supercache", Version)
		os.Exit(0)
	}

	if configPath == "" {
		configPath = os.Getenv("SUPERCACHE_CONFIG")
	}
	if configPath == "" {
		if _, err := os.Stat(config.DefaultConfigPath); err == nil {
			configPath = config.DefaultConfigPath
		} else if _, err := os.Stat(config.DefaultConfigPathAlt); err == nil {
			configPath = config.DefaultConfigPathAlt
		}
	}
	if configPath == "" {
		fmt.Fprintln(os.Stderr, "supercache: no configuration file specified; use -config or set SUPERCACHE_CONFIG")
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supercache: %v\n", err)
		os.Exit(1)
	}

	// Merge persisted peer state from previous run if available.
	if ps := cfg.PeerStateFile; ps != "" {
		if saved, loadErr := peer.LoadPeerStateFile(ps); loadErr == nil && len(saved) > 0 {
			cfg.Peers = config.MergePeerLists(cfg.Peers, saved)
		}
	}

	logCleanup, err := logging.Init(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supercache: logging init: %v\n", err)
		os.Exit(1)
	}
	defer logCleanup()

	srv, err := server.New(cfg)
	if err != nil {
		slog.Error("server init failed", "err", err)
		os.Exit(1)
	}
	srv.SetConfigPath(configPath)
	srv.SetBuildVersion(Version)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.SetRunCancel(cancel)

	// SIGHUP triggers hot-reload; SIGINT/SIGTERM trigger graceful shutdown.
	srv.SetOnReload(func(changed []string) {
		slog.Info("hot-reload applied", "changed", changed)
		// Re-init logging in case log_level/log_output/log_format changed.
		newCfg := srv.CurrentConfig()
		if newCfg != nil {
			lc, lerr := logging.Init(newCfg)
			if lerr != nil {
				slog.Error("logging re-init after reload", "err", lerr)
			} else {
				logCleanup()
				logCleanup = lc
			}
		}
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGHUP:
				slog.Info("received SIGHUP, reloading configuration")
				if _, err := srv.ReloadConfig(); err != nil {
					slog.Error("config reload failed", "err", err)
				}
			case syscall.SIGINT, syscall.SIGTERM:
				slog.Info("received shutdown signal", "signal", sig)
				if err := srv.Shutdown(ctx); err != nil {
					slog.Error("shutdown error", "err", err)
				}
				cancel()
				return
			}
		}
	}()

	slog.Info("starting supercache",
		"version", Version,
		"config", configPath,
		"client_addr", fmt.Sprintf("%s:%d", cfg.ClientBind, cfg.ClientPort),
		"peer_addr", fmt.Sprintf("%s:%d", cfg.PeerBind, cfg.PeerPort),
	)

	if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}
