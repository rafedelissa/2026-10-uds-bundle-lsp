// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/defenseunicorns/uds-cli/internal/lsp"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFmtCommandFormatsWorkspace(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, bundleFileName)
	require.NoError(t, os.WriteFile(filename, []byte("metadata {\nname=\"demo\"\n}\n"), 0o600))
	require.NoError(t, os.Chmod(filename, 0o640))
	streams, _, out, _ := iostreams.NewTestIOStreams()
	cmd := NewBundleCommand(streams)
	cmd.SetArgs([]string{"fmt", root})

	require.NoError(t, cmd.ExecuteContext(t.Context()))

	formatted, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.Equal(t, "metadata {\n  name = \"demo\"\n}\n", string(formatted))
	assert.Equal(t, "Formatted bundle.uds.hcl\n", out.String())
	info, err := os.Stat(filename)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}

func TestWriteFormattedDocumentRejectsChangedSource(t *testing.T) {
	filename := filepath.Join(t.TempDir(), bundleFileName)
	const original = "metadata {\nname=\"original\"\n}\n"
	const newer = "metadata {\n  name = \"newer\"\n}\n"
	require.NoError(t, os.WriteFile(filename, []byte(newer), 0o600))

	err := writeFormattedDocument(lsp.FormattedDocument{
		Path: filename, OriginalText: original, Text: "metadata {\n  name = \"original\"\n}\n",
	})

	require.ErrorIs(t, err, ErrFormatConflict)
	content, readErr := os.ReadFile(filename)
	require.NoError(t, readErr)
	assert.Equal(t, newer, string(content))
}

func TestWriteFormattedDocumentPreservesSpecialModeBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix set-ID mode bits")
	}
	filename := filepath.Join(t.TempDir(), bundleFileName)
	const original = "metadata {\nname=\"demo\"\n}\n"
	const formatted = "metadata {\n  name = \"demo\"\n}\n"
	require.NoError(t, os.WriteFile(filename, []byte(original), 0o600))
	mode := os.FileMode(0o640) | os.ModeSetuid | os.ModeSetgid
	require.NoError(t, os.Chmod(filename, mode))
	before, err := os.Stat(filename)
	require.NoError(t, err)
	if before.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid) != mode {
		t.Skip("filesystem does not retain set-ID mode bits")
	}

	require.NoError(t, writeFormattedDocument(lsp.FormattedDocument{Path: filename, OriginalText: original, Text: formatted}))

	after, err := os.Stat(filename)
	require.NoError(t, err)
	assert.Equal(t, mode, after.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid))
}

func TestWriteFormattedDocumentPreservesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.uds.hcl")
	link := filepath.Join(root, bundleFileName)
	const original = "metadata {\nname=\"demo\"\n}\n"
	const formatted = "metadata {\n  name = \"demo\"\n}\n"
	require.NoError(t, os.WriteFile(target, []byte(original), 0o600))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	require.NoError(t, writeFormattedDocument(lsp.FormattedDocument{Path: link, OriginalText: original, Text: formatted}))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, formatted, string(content))
}

func TestFmtCommandReportsCleanWorkspace(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, bundleFileName), []byte("metadata {\n  name = \"demo\"\n}\n"), 0o600))
	streams, _, out, _ := iostreams.NewTestIOStreams()
	cmd := NewBundleCommand(streams)
	cmd.SetArgs([]string{"fmt", root})

	require.NoError(t, cmd.ExecuteContext(t.Context()))
	assert.Equal(t, "No formatting changes required.\n", out.String())
}

func TestFmtCommandPrintsStructuredResult(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, bundleFileName), []byte("metadata {\nname=\"demo\"\n}\n"), 0o600))
	streams, _, out, _ := iostreams.NewTestIOStreams()
	cmd := NewBundleCommand(streams)
	cmd.SetArgs([]string{"fmt", root, "--output", "json"})

	require.NoError(t, cmd.ExecuteContext(t.Context()))
	var result fmtResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	assert.Equal(t, []string{bundleFileName}, result.Formatted)
}

func TestFmtCommandRejectsEmptyPathAndUnsupportedFlags(t *testing.T) {
	for _, args := range [][]string{
		{"fmt", ""},
		{"fmt", "--architecture", "amd64"},
	} {
		cmd := NewBundleCommand(iostreams.IOStreams{})
		cmd.SetArgs(args)

		err := cmd.ExecuteContext(t.Context())

		require.ErrorIs(t, err, ErrInvalidArgument)
	}
}
