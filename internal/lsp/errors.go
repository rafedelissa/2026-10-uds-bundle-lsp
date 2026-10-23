// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import "errors"

var (
	// ErrExitBeforeShutdown indicates that a client sent exit without first sending shutdown.
	ErrExitBeforeShutdown = errors.New("LSP client exited before shutdown")
	// ErrNoBundleDefinitions indicates that a lint workspace contains no bundle definitions.
	ErrNoBundleDefinitions = errors.New("no bundle.uds.hcl files found")
)
