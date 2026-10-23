// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLintUsesWorkspaceDiagnostics(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "bundle.uds.hcl"), []byte(`package "app" {`), 0o600))

	diagnostics, err := Lint(t.Context(), root)

	require.NoError(t, err)
	require.NotEmpty(t, diagnostics)
	assert.Equal(t, "bundle.uds.hcl", diagnostics[0].Path)
	assert.Equal(t, LintSeverityError, diagnostics[0].Severity)
	assert.Positive(t, diagnostics[0].Line)
	assert.Positive(t, diagnostics[0].Column)
}

func TestLintRejectsWorkspaceWithoutBundle(t *testing.T) {
	diagnostics, err := Lint(t.Context(), t.TempDir())

	require.ErrorIs(t, err, ErrNoBundleDefinitions)
	assert.Nil(t, diagnostics)
}

func TestLintRejectsFilePath(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "bundle.uds.hcl")
	require.NoError(t, os.WriteFile(filename, nil, 0o600))

	diagnostics, err := Lint(t.Context(), filename)

	require.ErrorContains(t, err, "is not a directory")
	assert.Nil(t, diagnostics)
}
