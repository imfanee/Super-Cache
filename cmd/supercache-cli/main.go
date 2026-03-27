// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Command supercache-cli is the Super-Cache management CLI.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/mgmt"
)

// Version is injected at link time via -ldflags "-X main.Version=...".
var Version = "dev"

// formatOutput prints either raw JSON (MgmtResponse) or a human-readable view of the payload.
func formatOutput(resp mgmt.MgmtResponse, jsonMode bool) error {
	return formatOutputTo(os.Stdout, resp, jsonMode)
}

func formatOutputTo(w io.Writer, resp mgmt.MgmtResponse, jsonMode bool) error {
	if jsonMode {
		b, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(b))
		return err
	}
	if !resp.OK {
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}
		return fmt.Errorf("request failed")
	}
	if resp.Data == nil {
		_, err := fmt.Fprintln(w, "OK")
		return err
	}
	b, err := json.MarshalIndent(resp.Data, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func resolveConfigPath(flagPath string) string {
	p := strings.TrimSpace(flagPath)
	if p != "" {
		return p
	}
	p = strings.TrimSpace(os.Getenv("SUPERCACHE_CONFIG"))
	if p != "" {
		return p
	}
	if p := config.FirstExistingDefaultConfigPath(); p != "" {
		return p
	}
	return ""
}

func main() {
	configPath := flag.String("config", "", "path to TOML or YAML (optional; also SUPERCACHE_CONFIG or first existing of "+config.DefaultConfigPath+" / "+config.DefaultConfigPathAlt+")")
	socket := flag.String("socket", os.Getenv("SUPERCACHE_MGMT_SOCKET"), "Unix socket path (default: from config or /var/run/supercache.sock)")
	mgmtTCP := flag.String("mgmt-tcp", os.Getenv("SUPERCACHE_MGMT_TCP"), "loopback host:port for management API (overrides Unix socket; optional)")
	secret := flag.String("secret", os.Getenv("SUPERCACHE_SECRET"), "cluster shared_secret (or set env SUPERCACHE_SECRET)")
	jsonMode := flag.Bool("json", false, "print raw MgmtResponse JSON to stdout")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "supercache-cli version %s\n", Version)
		fmt.Fprintf(os.Stderr, "usage: supercache-cli [-config path] [-socket path] [-mgmt-tcp host:port] [-secret s] <command> [args...]\n")
		fmt.Fprintf(os.Stderr, "commands: ping, info, status, reload-config, peers list|add|remove, shutdown, debug keyspace, bootstrap-status\n")
		os.Exit(2)
	}

	sec := strings.TrimSpace(*secret)
	sock := strings.TrimSpace(*socket)
	cfgPath := resolveConfigPath(*configPath)

	var loaded *config.Config
	if cfgPath != "" {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load config: %v\n", err)
			os.Exit(1)
		}
		loaded = cfg
		if sec == "" {
			sec = cfg.SharedSecret
		}
		if sock == "" && cfg.MgmtSocket != "" && cfg.MgmtSocket != "-" {
			sock = cfg.MgmtSocket
		}
	}
	if sock == "" {
		sock = "/var/run/supercache.sock"
	}
	if sec == "" {
		fmt.Fprintf(os.Stderr, "missing secret: use -secret, SUPERCACHE_SECRET, or -config with shared_secret\n")
		os.Exit(1)
	}

	mgmtNet := "unix"
	mgmtAddr := sock
	if mt := strings.TrimSpace(*mgmtTCP); mt != "" {
		mgmtNet = "tcp"
		mgmtAddr = mt
	} else if loaded != nil && loaded.MgmtTCPPort > 0 {
		bind := strings.TrimSpace(loaded.MgmtTCPBind)
		if bind == "" {
			bind = config.DefaultMgmtTCPBind
		}
		mgmtNet = "tcp"
		mgmtAddr = net.JoinHostPort(bind, fmt.Sprintf("%d", loaded.MgmtTCPPort))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := strings.ToLower(strings.TrimSpace(args[0]))
	switch cmd {
	case "ping":
		out, err := mgmt.ClientPing(ctx, mgmtNet, mgmtAddr, sec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if err := formatOutput(mgmt.MgmtResponse{OK: true, Data: out}, *jsonMode); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "info":
		m, err := mgmt.ClientInfo(ctx, mgmtNet, mgmtAddr, sec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if err := formatOutput(mgmt.MgmtResponse{OK: true, Data: m}, *jsonMode); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "status":
		m, err := mgmt.ClientStatus(ctx, mgmtNet, mgmtAddr, sec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if err := formatOutput(mgmt.MgmtResponse{OK: true, Data: m}, *jsonMode); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "reload-config":
		if err := mgmt.ClientReloadConfig(ctx, mgmtNet, mgmtAddr, sec); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Println("OK")
	case "peers":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: peers list|add <addr>|remove <addr>\n")
			os.Exit(2)
		}
		sub := strings.ToLower(strings.TrimSpace(args[1]))
		switch sub {
		case "list":
			rows, err := mgmt.ClientPeersList(ctx, mgmtNet, mgmtAddr, sec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			if err := formatOutput(mgmt.MgmtResponse{OK: true, Data: rows}, *jsonMode); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		case "add":
			if len(args) < 3 {
				fmt.Fprintf(os.Stderr, "usage: peers add <host:port>\n")
				os.Exit(2)
			}
			addr := strings.TrimSpace(args[2])
			if err := mgmt.ClientPeersAdd(ctx, mgmtNet, mgmtAddr, sec, addr); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			fmt.Println("OK")
		case "remove":
			if len(args) < 3 {
				fmt.Fprintf(os.Stderr, "usage: peers remove <host:port>\n")
				os.Exit(2)
			}
			addr := strings.TrimSpace(args[2])
			if err := mgmt.ClientPeersRemove(ctx, mgmtNet, mgmtAddr, sec, addr); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			fmt.Println("OK")
		default:
			fmt.Fprintf(os.Stderr, "unknown peers subcommand %q\n", sub)
			os.Exit(2)
		}
	case "shutdown":
		graceful := true
		for _, a := range args[1:] {
			switch strings.ToLower(strings.TrimSpace(a)) {
			case "--immediate", "--force", "-i":
				graceful = false
			}
		}
		if err := mgmt.ClientShutdown(ctx, mgmtNet, mgmtAddr, sec, graceful); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Println("OK")
	case "debug":
		if len(args) < 2 || strings.ToLower(args[1]) != "keyspace" {
			fmt.Fprintf(os.Stderr, "usage: debug keyspace [--count N]\n")
			os.Exit(2)
		}
		count := 10
		for i := 2; i < len(args); i++ {
			if args[i] == "--count" && i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil {
					fmt.Fprintf(os.Stderr, "bad count: %v\n", err)
					os.Exit(2)
				}
				count = n
				i++
			}
		}
		m, err := mgmt.ClientDebugKeyspace(ctx, mgmtNet, mgmtAddr, sec, count)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if err := formatOutput(mgmt.MgmtResponse{OK: true, Data: m}, *jsonMode); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "bootstrap-status":
		m, err := mgmt.ClientBootstrapStatus(ctx, mgmtNet, mgmtAddr, sec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if err := formatOutput(mgmt.MgmtResponse{OK: true, Data: m}, *jsonMode); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		os.Exit(2)
	}
}

