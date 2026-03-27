// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Package logging configures the process-wide slog logger from Super-Cache config.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/supercache/supercache/internal/config"
)

// Init sets the default slog logger from cfg (log_level, log_output, log_format). The returned
// cleanup closes any opened log file; it is safe to call Init again after cleanup.
func Init(cfg *config.Config) (cleanup func(), err error) {
	w, c, err := writerFor(cfg)
	if err != nil {
		return func() {}, err
	}
	lvl := parseLevel(cfg.NormalizeLogLevel())
	opts := &slog.HandlerOptions{Level: lvl}

	var h slog.Handler
	switch strings.ToLower(strings.TrimSpace(cfg.LogFormat)) {
	case "", "text":
		h = slog.NewTextHandler(w, opts)
	case "json":
		h = slog.NewJSONHandler(w, opts)
	case "logfmt":
		// slog text handler emits key=value lines compatible with common logfmt consumers (NFR-031).
		h = slog.NewTextHandler(w, opts)
	default:
		h = slog.NewTextHandler(w, opts)
	}
	slog.SetDefault(slog.New(h))
	return c, nil
}

func writerFor(cfg *config.Config) (io.Writer, func(), error) {
	lo := strings.ToLower(strings.TrimSpace(cfg.LogOutput))
	switch lo {
	case "", "stdout":
		return os.Stdout, func() {}, nil
	case "stderr":
		return os.Stderr, func() {}, nil
	default:
		f, err := cfg.OpenLogFile()
		if err != nil {
			return nil, func() {}, err
		}
		return f, func() { _ = f.Close() }, nil
	}
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
