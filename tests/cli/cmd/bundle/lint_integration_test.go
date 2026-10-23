// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

//go:build cli

package bundle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLintCommandRejectsUnsupportedRootFlag_Integration(t *testing.T) {
	output, err := executeCLI(t, "", "bundle", "lint", "--prompt")

	require.ErrorContains(t, err, "--prompt is not supported by bundle lint")
	assert.Empty(t, output)
}

func TestLintCommand_Integration(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, bundle.BundleFileName), []byte(`package "app" {`), 0o600))

	output, err := executeCLI(t, "", "bundle", "lint", root)

	require.Error(t, err)
	assert.Contains(t, output, "bundle.uds.hcl:1:")
	assert.Contains(t, output, ": error:")
}
