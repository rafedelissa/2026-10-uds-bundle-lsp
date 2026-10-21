// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import "errors"

// ErrExitBeforeShutdown indicates that a client sent exit without first sending shutdown.
var ErrExitBeforeShutdown = errors.New("LSP client exited before shutdown")
