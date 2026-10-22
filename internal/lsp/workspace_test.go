package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeWorkspaceStartupDiagnostics(t *testing.T) {
	root, _, _, valuesURI := sharedDiagnosticWorkspace(t)
	input := framedMessages(t,
		encodeMessage(t, "initialize", 1, map[string]any{"workspaceFolders": []workspaceFolder{{URI: valuesFileURI(root), Name: "test"}}}),
		encodeMessage(t, "initialized", nil, map[string]any{}),
		encodeMessage(t, "shutdown", 2, nil),
		encodeMessage(t, "exit", nil, nil),
	)
	var output bytes.Buffer
	require.NoError(t, Serve(t.Context(), input, &output, iostreams.IOStreams{}))
	var found bool
	reader := bufio.NewReader(&output)
	for {
		payload, err := readMessage(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		var msg message
		require.NoError(t, json.Unmarshal(payload, &msg))
		if msg.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var publication publishDiagnosticsParams
		require.NoError(t, json.Unmarshal(msg.Params, &publication))
		assert.Nil(t, publication.Version, "closed files have no buffer version")
		if publication.URI == valuesURI {
			require.Len(t, publication.Diagnostics, 1)
			assert.Contains(t, publication.Diagnostics[0].Message, "shared-b")
			assert.Contains(t, publication.Diagnostics[0].Message, `no entry for key "message"`)
			found = true
		}
	}
	assert.True(t, found, "publish diagnostics before any files are opened")
}

func TestWorkspaceRootSelection(t *testing.T) {
	root, _, _, valuesURI := sharedDiagnosticWorkspace(t)
	other := t.TempDir()
	for _, tt := range []struct {
		name   string
		params map[string]any
		want   bool
	}{
		{name: "root URI", params: map[string]any{"rootUri": valuesFileURI(root)}, want: true},
		{name: "legacy root path", params: map[string]any{"rootPath": root}, want: true},
		{name: "workspace folders take precedence", params: map[string]any{"workspaceFolders": []workspaceFolder{{URI: valuesFileURI(root)}}, "rootUri": valuesFileURI(other)}, want: true},
		{name: "explicitly empty folders", params: map[string]any{"workspaceFolders": []workspaceFolder{}, "rootUri": valuesFileURI(root)}},
		{name: "null folders use root URI", params: map[string]any{"workspaceFolders": nil, "rootUri": valuesFileURI(root)}, want: true},
		{name: "nonlocal folders", params: map[string]any{"workspaceFolders": []workspaceFolder{{URI: "vscode-remote://host/workspace"}}}},
		{name: "no root", params: map[string]any{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.params)
			require.NoError(t, err)
			var params initializeParams
			require.NoError(t, json.Unmarshal(encoded, &params))
			store := newDocumentStore()
			store.initializeWorkspace(params)
			require.NoError(t, store.indexWorkspace(t.Context()))
			results := store.workspaceDiagnostics(t.Context())
			if tt.want {
				require.Len(t, results[valuesURI], 1)
			} else {
				assert.Empty(t, results)
			}
		})
	}
}

func TestIndexedWorkspaceBufferOverlay(t *testing.T) {
	root, _, bURI, valuesURI := sharedDiagnosticWorkspace(t)
	store := newDocumentStore()
	store.diagnosticVersionSupport = true
	store.initializeWorkspace(initializeParams{RootURI: valuesFileURI(root)})
	publications := diagnosticNotification(t, store, "initialized", map[string]any{})
	require.Len(t, publications[valuesURI].Diagnostics, 1)

	configURI := valuesFileURI(filepath.Join(root, "shared-b", "config.uds.hcl"))
	publications = diagnosticNotification(t, store, "textDocument/didOpen", didOpenTextDocumentParams{
		TextDocument: textDocumentItem{URI: configURI, Version: 1, Text: `variables = { message = "unsaved config" }`},
	})
	assert.Empty(t, publications[valuesURI].Diagnostics, "unsaved config applies to unopened bundles")
	publications = diagnosticNotification(t, store, "textDocument/didClose", didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: configURI}})
	require.Len(t, publications[valuesURI].Diagnostics, 1, "closing config restores disk variables")

	publications = diagnosticNotification(t, store, "textDocument/didOpen", didOpenTextDocumentParams{
		TextDocument: textDocumentItem{URI: valuesURI, Version: 3, Text: "message: static\n"},
	})
	assert.Empty(t, publications[valuesURI].Diagnostics)
	require.NotNil(t, publications[valuesURI].Version)
	assert.Equal(t, int32(3), *publications[valuesURI].Version)
	publications = diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": valuesURI, "type": 2}}})
	assert.Empty(t, publications[valuesURI].Diagnostics, "disk notifications must not replace open buffers")
	publications = diagnosticNotification(t, store, "textDocument/didClose", didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: valuesURI}})
	require.Len(t, publications[valuesURI].Diagnostics, 1, "closing every tab keeps indexed diagnostics")
	assert.Nil(t, publications[valuesURI].Version)

	bPath, ok := fileURIPath(bURI)
	require.True(t, ok)
	text, err := os.ReadFile(bPath)
	require.NoError(t, err)
	publications = diagnosticNotification(t, store, "textDocument/didOpen", didOpenTextDocumentParams{
		TextDocument: textDocumentItem{URI: bURI, Version: 1, Text: strings.Replace(string(text), `values_files = ["../shared/values.yaml"]`, "values_files = []", 1)},
	})
	assert.Empty(t, publications[valuesURI].Diagnostics, "unsaved bundle references override the index")
	publications = diagnosticNotification(t, store, "textDocument/didClose", didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: bURI}})
	require.Len(t, publications[valuesURI].Diagnostics, 1)
}

func TestWorkspaceFolderChanges(t *testing.T) {
	root, _, _, valuesURI := sharedDiagnosticWorkspace(t)
	other := t.TempDir()
	otherConfig := filepath.Join(other, "config.uds.hcl")
	writeDiagnosticFile(t, otherConfig, `variables = { nested = null }`)
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{RootURI: valuesFileURI(root)})
	diagnosticNotification(t, store, "initialized", map[string]any{})
	publications := diagnosticNotification(t, store, "workspace/didChangeWorkspaceFolders", map[string]any{"event": map[string]any{
		"added": []workspaceFolder{{URI: valuesFileURI(other)}},
	}})
	require.Len(t, publications[valuesFileURI(otherConfig)].Diagnostics, 1)
	require.Len(t, publications[valuesURI].Diagnostics, 1, "adding a root preserves the original root")
	publications = diagnosticNotification(t, store, "workspace/didChangeWorkspaceFolders", map[string]any{"event": map[string]any{
		"removed": []workspaceFolder{{URI: valuesFileURI(root)}},
	}})
	assert.Empty(t, publications[valuesURI].Diagnostics, "removing a root clears stale shared-file results")
	require.Len(t, publications[valuesFileURI(otherConfig)].Diagnostics, 1)
	publications = diagnosticNotification(t, store, "workspace/didChangeWorkspaceFolders", map[string]any{"event": map[string]any{
		"removed": []workspaceFolder{{URI: valuesFileURI(other)}},
	}})
	for _, publication := range publications {
		assert.Empty(t, publication.Diagnostics)
	}
}

func TestWorkspaceBundleFileEvents(t *testing.T) {
	root := t.TempDir()
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{RootURI: valuesFileURI(root)})
	diagnosticNotification(t, store, "initialized", map[string]any{})
	dir := filepath.Join(root, "new")
	bundleURI := writeIndexedBundle(t, dir, "missing.yaml", `variables = {}`)
	publications := diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": valuesFileURI(dir), "type": 1}}})
	require.Len(t, publications[bundleURI].Diagnostics, 1, "discover bundles in newly created directories")

	renamed := filepath.Join(root, "renamed")
	require.NoError(t, os.Rename(dir, renamed))
	publications = diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{
		map[string]any{"uri": valuesFileURI(dir), "type": 3}, map[string]any{"uri": valuesFileURI(renamed), "type": 1},
	}})
	assert.Empty(t, publications[bundleURI].Diagnostics)
	renamedBundle := filepath.Join(renamed, "bundle.uds.hcl")
	require.Len(t, publications[valuesFileURI(renamedBundle)].Diagnostics, 1)
	require.NoError(t, os.Remove(renamedBundle))
	publications = diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": valuesFileURI(renamedBundle), "type": 3}}})
	assert.Empty(t, publications[valuesFileURI(renamedBundle)].Diagnostics, "deleting an unopened bundle clears its results")
}

func TestWorkspaceSavedNewBundle(t *testing.T) {
	root := t.TempDir()
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{RootURI: valuesFileURI(root)})
	diagnosticNotification(t, store, "initialized", map[string]any{})
	bundleURI := writeIndexedBundle(t, root, "missing.yaml", `variables = {}`)
	text, err := os.ReadFile(filepath.Join(root, "bundle.uds.hcl"))
	require.NoError(t, err)
	diagnosticNotification(t, store, "textDocument/didOpen", didOpenTextDocumentParams{TextDocument: textDocumentItem{URI: bundleURI, Version: 1, Text: string(text)}})
	diagnosticNotification(t, store, "textDocument/didSave", didSaveTextDocumentParams{TextDocument: textDocumentIdentifier{URI: bundleURI}})
	publications := diagnosticNotification(t, store, "textDocument/didClose", didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: bundleURI}})
	require.Len(t, publications[bundleURI].Diagnostics, 1, "a saved new bundle remains indexed without file watchers")
}

func TestWorkspaceScanBoundaries(t *testing.T) {
	root, aURI, bURI, valuesURI := sharedDiagnosticWorkspace(t)
	for _, name := range []string{".git", "vendor", "node_modules", "build", "dist"} {
		writeIndexedBundle(t, filepath.Join(root, name, "nested"), "missing.yaml", `variables = {}`)
	}
	outside := t.TempDir()
	writeIndexedBundle(t, outside, "missing.yaml", `variables = {}`)
	standalonePath := filepath.Join(root, "config.uds.hcl")
	writeDiagnosticFile(t, standalonePath, `variables = null`)
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{WorkspaceFolders: []workspaceFolder{
		{URI: valuesFileURI(root)}, {URI: valuesFileURI(filepath.Join(root, "shared-b"))},
	}})
	require.NoError(t, store.indexWorkspace(t.Context()))
	results := store.workspaceDiagnostics(t.Context())
	assert.Len(t, results, 6, "two bundles, two defaults, a shared values file, and standalone config")
	assert.Contains(t, results, aURI)
	assert.Contains(t, results, bURI)
	require.Len(t, results[valuesURI], 1, "overlapping roots must not duplicate shared-file diagnostics")
	require.Len(t, results[valuesFileURI(standalonePath)], 1)
	assert.False(t, store.workspaceIncludes(filepath.Join(root, "build", "bundle.uds.hcl")))
	assert.False(t, store.workspaceIncludes(filepath.Join(outside, "bundle.uds.hcl")))
	assert.True(t, store.workspaceIncludes(filepath.Join(root, "shared-b", "bundle.uds.hcl")))
}

func TestWorkspaceScanDoesNotFollowDirectorySymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeIndexedBundle(t, outside, "missing.yaml", `variables = {}`)
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{RootURI: valuesFileURI(root)})
	require.NoError(t, store.indexWorkspace(t.Context()))
	assert.Empty(t, store.workspaceDiagnostics(t.Context()))
}

func TestWorkspaceScanCancellation(t *testing.T) {
	root := t.TempDir()
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{RootURI: valuesFileURI(root)})
	store.workspaceFiles = map[string]struct{}{"previous": {}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, store.indexWorkspace(ctx), context.Canceled)
	assert.Contains(t, store.workspaceFiles, "previous")
}

func TestWorkspaceScanMissingRoot(t *testing.T) {
	root, _, _, valuesURI := sharedDiagnosticWorkspace(t)
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{WorkspaceFolders: []workspaceFolder{
		{URI: valuesFileURI(filepath.Join(root, "does-not-exist"))}, {URI: valuesFileURI(root)},
	}})
	require.ErrorIs(t, store.indexWorkspace(t.Context()), os.ErrNotExist)
	results := store.workspaceDiagnostics(t.Context())
	require.Len(t, results[valuesURI], 1, "one inaccessible root must not hide valid roots")
}

func sharedDiagnosticWorkspace(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "shared"), 0o700))
	valuesPath := filepath.Join(root, "shared", "values.yaml")
	writeDiagnosticFile(t, valuesPath, "message: {{ .vars.message }}\n")
	aURI := writeIndexedBundle(t, filepath.Join(root, "shared-a"), "../shared/values.yaml", `variables = { message = "from a" }`)
	bURI := writeIndexedBundle(t, filepath.Join(root, "shared-b"), "../shared/values.yaml", `variables = { other = true }`)
	return root, aURI, bURI, valuesFileURI(valuesPath)
}

func writeIndexedBundle(t *testing.T, dir, valuesPath, defaults string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	text := strings.Replace(validBundle, `source = "${local.repo}/app:${sys.arch}"`, fmt.Sprintf("source = \"example\"\n  values_files = [%q]", filepath.ToSlash(valuesPath)), 1)
	filename := filepath.Join(dir, "bundle.uds.hcl")
	writeDiagnosticFile(t, filename, text)
	writeDiagnosticFile(t, filepath.Join(dir, "defaults.uds.hcl"), defaults)
	return valuesFileURI(filename)
}
