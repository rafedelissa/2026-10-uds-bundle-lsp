// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

// Package main is the entrypoint for the UDS language server.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/defenseunicorns/uds-cli/internal/logger"
	"github.com/defenseunicorns/uds-cli/internal/lsp"
)

func main() {
	if err := lsp.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		logger.New(os.Stderr, slog.LevelError).Error("LSP server stopped", "error", err)
		os.Exit(1)
	}
}
