// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

// Package main is the entrypoint for the UDS language server.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/defenseunicorns/uds-cli/internal/logger"
	"github.com/defenseunicorns/uds-cli/internal/lsp"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
)

func main() {
	filename := os.Getenv("UDS_LSP_LOG_FILE")
	if filename == "" {
		filename = filepath.Join(os.TempDir(), "uds-lsp.log")
	}
	stderrLogger := logger.New(os.Stderr, slog.LevelInfo)
	logFile, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // The user selects this log path through the process environment, not LSP content.
	if err != nil {
		stderrLogger.Error("Cannot open LSP log file", "path", filename, "error", err)
		os.Exit(1)
	}

	l := logger.New(io.MultiWriter(os.Stderr, logFile), slog.LevelInfo).With("pid", os.Getpid())
	streams := iostreams.New(nil, nil, nil).WithLogger(l, nil)
	streams.Info("LSP server starting", "log_file", filename)
	serveErr := lsp.Serve(context.Background(), os.Stdin, os.Stdout, streams)
	if serveErr != nil {
		streams.Error("LSP server stopped", "error", serveErr)
	} else {
		streams.Info("LSP server stopped")
	}
	closeErr := logFile.Close()
	if closeErr != nil {
		stderrLogger.Error("Cannot close LSP log file", "error", closeErr)
	}
	if serveErr != nil || closeErr != nil {
		os.Exit(1)
	}
}
