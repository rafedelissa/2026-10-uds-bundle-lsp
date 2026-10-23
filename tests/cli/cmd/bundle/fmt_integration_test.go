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

func TestFmtCheckCommand_Integration(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, bundle.BundleFileName)
	const source = "metadata {\nname=\"demo\"\n}\n"
	require.NoError(t, os.WriteFile(filename, []byte(source), 0o600))

	output, err := executeCLI(t, "", "bundle", "fmt", root, "--check")

	require.Error(t, err)
	assert.Equal(t, "Would format bundle.uds.hcl\n", output)
	content, readErr := os.ReadFile(filename)
	require.NoError(t, readErr)
	assert.Equal(t, source, string(content))
}

func TestFmtCommand_Integration(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, bundle.BundleFileName)
	require.NoError(t, os.WriteFile(filename, []byte("metadata {\nname=\"demo\"\n}\n"), 0o600))

	output, err := executeCLI(t, "", "bundle", "fmt", root)

	require.NoError(t, err)
	assert.Equal(t, "Formatted bundle.uds.hcl\n", output)
	formatted, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.Equal(t, "metadata {\n  name = \"demo\"\n}\n", string(formatted))
}
