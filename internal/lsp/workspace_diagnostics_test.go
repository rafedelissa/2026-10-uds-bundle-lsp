package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceValuesDiagnostics(t *testing.T) {
	tests := []struct {
		name     string
		defaults string
		config   string
		values   string
		file     string
		want     string
	}{
		{
			name:     "nested objects merge and config wins",
			defaults: `variables = { db = { host = "local", port = 5432 } }`,
			config:   `variables = { db = { port = 6432 } }`,
			values:   `{{ if or (ne .vars.db.host "local") (ne .vars.db.port 6432.0) }}{{ .vars.failed }}{{ end }}`,
		},
		{
			name:     "collections replace wholesale",
			defaults: `variables = { ports = [{name = "old"}, {name = "extra"}] }`,
			config:   `variables = { ports = [{name = "new"}] }`,
			values:   `{{ range .vars.ports }}{{ if ne .name "new" }}{{ $.vars.failed }}{{ end }}{{ end }}`,
		},
		{
			name: "missing nested variable", defaults: `variables = { db = {} }`,
			values: "# 😀 template\nhost: {{ .vars.db.host }}\n", file: "values/app.yaml", want: `no entry for key "host"`,
		},
		{
			name: "scalar replaces an object", defaults: `variables = { db = {host = "local"} }`,
			config: `variables = { db = "external" }`, values: `host: {{ .vars.db.host }}`,
			file: "values/app.yaml", want: "can't evaluate field host",
		},
		{
			name: "range requires a suitable type", defaults: `variables = { ports = true }`,
			values: `{{ range .vars.ports }}port: {{ . }}{{ end }}`, file: "values/app.yaml", want: "range can't iterate",
		},
		{
			name: "collection element scope", defaults: `variables = { ports = [{name = "app"}] }`,
			values: `{{ range .vars.ports }}port: {{ .port }}{{ end }}`, file: "values/app.yaml", want: `no entry for key "port"`,
		},
		{
			name: "unexecuted branch is valid", defaults: `variables = { enabled = false }`,
			values: `{{ if .vars.enabled }}{{ .vars.missing }}{{ end }}`,
		},
		{
			name: "template syntax", values: "before: true\n{{ range .vars.ports }}\n", file: "values/app.yaml", want: "unexpected EOF",
		},
		{
			name: "unknown function", values: `{{ unknown .vars }}`, file: "values/app.yaml", want: `function "unknown" not defined`,
		},
		{
			name: "nil variables skip execution", values: `value: {{ .vars.missing }}`,
		},
		{
			name: "empty object still executes", config: `variables = {}`, values: `value: {{ .vars.missing }}`,
			file: "values/app.yaml", want: `no entry for key "missing"`,
		},
		{
			name: "config conversion failure suppresses execution", config: `variables = { db = null }`,
			values: `host: {{ .vars.db.host }}`, file: "config.uds.hcl", want: "null values are not supported",
		},
		{
			name: "config syntax", config: "variables = {", values: `host: {{ .vars.db.host }}`,
			file: "config.uds.hcl", want: "Missing expression",
		},
		{
			name: "config option types", config: `options { concurrency = "many" }`,
			values: `host: {{ .vars.db.host }}`, file: "config.uds.hcl", want: "number is required",
		},
		{
			name: "defaults reject blocks", defaults: `options { concurrency = 2 }`,
			values: `host: {{ .vars.db.host }}`, file: "defaults.uds.hcl", want: "Unexpected",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, dir, bundleURI, valuesURI := newDiagnosticWorkspace(t)
			for name, text := range map[string]string{"defaults.uds.hcl": tt.defaults, "config.uds.hcl": tt.config, "values/app.yaml": tt.values} {
				// Leave conflicting disk files in place to verify unsaved buffers win.
				uri := valuesFileURI(filepath.Join(dir, name))
				store.documents[uri] = document{URI: uri, Text: text}
			}
			results := store.workspaceDiagnostics(t.Context())
			assert.Empty(t, results[bundleURI])
			if tt.want == "" {
				for uri, diagnostics := range results {
					assert.Empty(t, diagnostics, uri)
				}
				return
			}
			uri := valuesFileURI(filepath.Join(dir, tt.file))
			require.Len(t, results[uri], 1)
			d := results[uri][0]
			assert.Contains(t, d.Message, tt.want)
			assert.Equal(t, 1, d.Severity)
			if uri == valuesURI {
				require.Len(t, d.RelatedInformation, 3)
				assert.Equal(t, bundleURI, d.RelatedInformation[0].Location.URI)
				assert.Contains(t, d.Message, "adjacent defaults/config")
			} else {
				assert.Empty(t, results[valuesURI], "invalid variables must not cause cascading execution errors")
			}
			if tt.name == "missing nested variable" {
				assert.Equal(t, documentRange{Start: position{Line: 1, Character: 6}, End: position{Line: 1, Character: 25}}, d.Range)
			}
		})
	}
}

func TestWorkspaceValuesFilePaths(t *testing.T) {
	store, dir, bundleURI, _ := newDiagnosticWorkspace(t)
	shared := filepath.Join(t.TempDir(), "shared.yaml")
	writeDiagnosticFile(t, shared, "value: true\n")
	relative, err := filepath.Rel(dir, shared)
	require.NoError(t, err)
	bufferPath := filepath.Join(dir, "unsaved.yaml")
	bufferURI := valuesFileURI(bufferPath)
	store.documents[bufferURI] = document{URI: bufferURI, Text: "value: true\n"}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "directory.yaml"), 0o700))
	doc := store.documents[bundleURI]
	doc.Text = strings.Replace(doc.Text, `["values/app.yaml"]`, fmt.Sprintf(`[%q, %q, "unsaved.yaml", "missing.yaml", "directory.yaml"]`, filepath.ToSlash(shared), filepath.ToSlash(relative)), 1)
	store.documents[bundleURI] = doc

	results := store.workspaceDiagnostics(t.Context())

	require.Len(t, results[bundleURI], 2)
	assert.Contains(t, results[bundleURI][0].Message, "missing.yaml")
	assert.Contains(t, results[bundleURI][1].Message, "not a regular file")
	offset := strings.Index(doc.Text, `"missing.yaml"`)
	assert.Equal(t, documentRange{Start: positionAtByte(doc.Text, offset), End: positionAtByte(doc.Text, offset+len(`"missing.yaml"`))}, results[bundleURI][0].Range)
	assert.Empty(t, results[bufferURI])
}

func TestWorkspaceDiagnosticPathsAreRelative(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	store := newDocumentStore()
	store.workspaceFolders = map[string]workspaceFolder{
		root:   {URI: valuesFileURI(root)},
		nested: {URI: valuesFileURI(nested)},
	}
	filename := filepath.Join(nested, "bundle.uds.hcl")
	outside := filepath.Join(t.TempDir(), "external.yaml")
	results := map[string][]diagnostic{"file:///diagnostic": {{
		Message: fmt.Sprintf("read %q; bundle %s; external %q", filename, localFileURI(filename), outside),
	}}}

	store.relativizeDiagnosticMessages(results)

	message := results["file:///diagnostic"][0].Message
	assert.Contains(t, message, `read "bundle.uds.hcl"`)
	assert.Contains(t, message, "bundle bundle.uds.hcl")
	assert.Contains(t, message, outside)
	assert.NotContains(t, message, root)
}

func TestWorkspaceSharedValuesAndReferenceRemoval(t *testing.T) {
	store, dir, bundleURI, valuesURI := newDiagnosticWorkspace(t)
	values := store.documents[valuesURI]
	values.Text = "replicas: {{ .vars.missing }}\n"
	store.documents[valuesURI] = values
	otherDir := t.TempDir()
	otherURI := valuesFileURI(filepath.Join(otherDir, "bundle.uds.hcl"))
	store.documents[otherURI] = document{URI: otherURI, Text: strings.Replace(store.documents[bundleURI].Text, `"values/app.yaml"`, fmt.Sprintf("%q", filepath.ToSlash(filepath.Join(dir, "values/app.yaml"))), 1)}
	writeDiagnosticFile(t, filepath.Join(otherDir, "defaults.uds.hcl"), `variables = { other = true }`)

	publications := diagnosticNotification(t, store, "textDocument/didSave", didSaveTextDocumentParams{TextDocument: textDocumentIdentifier{URI: bundleURI}})
	require.Len(t, publications[valuesURI].Diagnostics, 2, "all referencing bundles must contribute")
	assert.NotEqual(t, publications[valuesURI].Diagnostics[0].RelatedInformation[0].Location.URI, publications[valuesURI].Diagnostics[1].RelatedInformation[0].Location.URI)

	for _, uri := range []string{bundleURI, otherURI} {
		doc := store.documents[uri]
		text := doc.Text[:strings.Index(doc.Text, "  values_files")] + "}\n"
		publications = diagnosticNotification(t, store, "textDocument/didChange", didChangeTextDocumentParams{
			TextDocument: versionedTextDocumentIdentifier{URI: uri, Version: 2}, ContentChanges: []textDocumentContentChangeEvent{{Text: text}},
		})
	}
	assert.Contains(t, publications, valuesURI)
	assert.Empty(t, publications[valuesURI].Diagnostics, "removing the final reference must clear stale diagnostics")
	results := store.workspaceDiagnostics(t.Context())
	assert.NotContains(t, results, valuesURI, "an unreferenced YAML file must not get UDS diagnostics")
}

func TestWorkspaceRevalidation(t *testing.T) {
	store, dir, bundleURI, valuesURI := newDiagnosticWorkspace(t)
	configURI := valuesFileURI(filepath.Join(dir, "config.uds.hcl"))
	publications := diagnosticNotification(t, store, "textDocument/didOpen", didOpenTextDocumentParams{
		TextDocument: textDocumentItem{URI: configURI, Version: 1, Text: `variables = { replicas = null }`},
	})
	require.NotEmpty(t, publications[configURI].Diagnostics)
	assert.Empty(t, publications[valuesURI].Diagnostics)

	publications = diagnosticNotification(t, store, "textDocument/didChange", didChangeTextDocumentParams{
		TextDocument:   versionedTextDocumentIdentifier{URI: configURI, Version: 2},
		ContentChanges: []textDocumentContentChangeEvent{{Text: `variables = { replicas = true }`}},
	})
	assert.Empty(t, publications[configURI].Diagnostics)
	assert.Empty(t, publications[valuesURI].Diagnostics)

	// Closing the unsaved config restores its disk version and revalidates users.
	publications = diagnosticNotification(t, store, "textDocument/didClose", didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: configURI}})
	assert.Empty(t, publications[configURI].Diagnostics)

	// A closed values file is loaded from disk. File events cover change, delete,
	// and create without touching the bundle's buffer or version.
	diagnosticNotification(t, store, "textDocument/didClose", didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: valuesURI}})
	writeDiagnosticFile(t, filepath.Join(dir, "values/app.yaml"), "value: {{ .vars.missing }}\n")
	publications = diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": valuesURI, "type": 2}}})
	require.NotEmpty(t, publications[valuesURI].Diagnostics)
	require.NoError(t, os.Remove(filepath.Join(dir, "values/app.yaml")))
	publications = diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": valuesURI, "type": 3}}})
	assert.Empty(t, publications[valuesURI].Diagnostics, "deleted files must clear their old template diagnostics")
	require.NotEmpty(t, publications[bundleURI].Diagnostics)
	writeDiagnosticFile(t, filepath.Join(dir, "values/app.yaml"), "value: {{ .vars.replicas }}\n")
	publications = diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": valuesURI, "type": 1}}})
	assert.Empty(t, publications[bundleURI].Diagnostics)
	assert.Empty(t, publications[valuesURI].Diagnostics)
}

func TestWorkspaceDiscoversBundleFromValues(t *testing.T) {
	store, dir, bundleURI, valuesURI := newDiagnosticWorkspace(t)
	delete(store.documents, bundleURI)
	writeDiagnosticFile(t, filepath.Join(dir, "defaults.uds.hcl"), `variables = { other = true }`)
	writeDiagnosticFile(t, filepath.Join(dir, "config.uds.hcl"), "")

	results := store.workspaceDiagnostics(t.Context())

	assert.Contains(t, results, bundleURI)
	require.NotEmpty(t, results[valuesURI])
	assert.Contains(t, results[valuesURI][0].Message, `no entry for key "replicas"`)
}

func TestWorkspaceUsesConfigArchitecture(t *testing.T) {
	store, dir, bundleURI, _ := newDiagnosticWorkspace(t)
	doc := store.documents[bundleURI]
	doc.Text = strings.Replace(doc.Text, `values/app.yaml`, `values/app-${sys.arch}.yaml`, 1)
	writeDiagnosticFile(t, filepath.Join(dir, "bundle.uds.hcl"), doc.Text)
	delete(store.documents, bundleURI)
	configURI := valuesFileURI(filepath.Join(dir, "config.uds.hcl"))
	store.documents[configURI] = document{URI: configURI, Text: "options { architecture = \"test-arch\" }\nvariables = { enabled = true }"}
	valuesURI := valuesFileURI(filepath.Join(dir, "values/app-test-arch.yaml"))
	store.documents[valuesURI] = document{URI: valuesURI, Text: "value: {{ .vars.missing }}"}

	results := store.workspaceDiagnostics(t.Context())

	assert.Empty(t, results[bundleURI])
	require.NotEmpty(t, results[valuesURI])
	assert.Contains(t, results[valuesURI][0].Message, `no entry for key "missing"`)
}

func TestTemplateDiagnosticUTF16Range(t *testing.T) {
	text := "# header\r\nlabel: 😀é {{ .vars.missing }}\r\n"
	d := valuesTemplateDiagnostic(document{URI: "file:///values.yaml", Text: text}, nil, true)
	assert.Nil(t, d, "nil variables skip execution")
	d = valuesTemplateDiagnostic(document{URI: "file:///values.yaml", Text: text}, bundle.Variables{}, true)
	require.NotNil(t, d)
	assert.Equal(t, documentRange{Start: position{Line: 1, Character: 11}, End: position{Line: 1, Character: 30}}, d.Range)
}

func TestStandaloneVariablesDiagnostics(t *testing.T) {
	store := newDocumentStore()
	uri := valuesFileURI(filepath.Join(t.TempDir(), "defaults.uds.hcl"))
	store.documents[uri] = document{URI: uri, Text: "variables = { nested = [null] }"}

	results := store.workspaceDiagnostics(t.Context())

	require.Len(t, results[uri], 1)
	assert.Contains(t, results[uri][0].Message, "null values are not supported")
	assert.NotEqual(t, documentRange{}, results[uri][0].Range)
}

func TestWorkspaceVariablesFileChanges(t *testing.T) {
	store, dir, bundleURI, valuesURI := newDiagnosticWorkspace(t)
	configPath := filepath.Join(dir, "config.uds.hcl")
	defaultsPath := filepath.Join(dir, "defaults.uds.hcl")
	configURI := valuesFileURI(configPath)
	defaultsURI := valuesFileURI(defaultsPath)
	writeDiagnosticFile(t, configPath, "")
	writeDiagnosticFile(t, defaultsPath, `variables = { other = true }`)
	publications := diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": defaultsURI, "type": 2}}})
	require.NotEmpty(t, publications[valuesURI].Diagnostics)

	writeDiagnosticFile(t, configPath, `variables = { replicas = 4 }`)
	publications = diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": configURI, "type": 2}}})
	assert.Empty(t, publications[valuesURI].Diagnostics)

	// Defaults are parsed independently, even when config supplies every value.
	writeDiagnosticFile(t, defaultsPath, `options { concurrency = 1 }`)
	publications = diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": defaultsURI, "type": 2}}})
	require.NotEmpty(t, publications[defaultsURI].Diagnostics)
	assert.Empty(t, publications[valuesURI].Diagnostics)
	require.NoError(t, os.Remove(defaultsPath))
	publications = diagnosticNotification(t, store, "workspace/didChangeWatchedFiles", map[string]any{"changes": []any{map[string]any{"uri": defaultsURI, "type": 3}}})
	assert.Empty(t, publications[defaultsURI].Diagnostics)
	assert.Empty(t, publications[bundleURI].Diagnostics)
	assert.Empty(t, publications[valuesURI].Diagnostics)
}

func TestWorkspaceDiagnosticVersionsAndClose(t *testing.T) {
	store, _, bundleURI, valuesURI := newDiagnosticWorkspace(t)
	store.diagnosticVersionSupport = true
	publications := diagnosticNotification(t, store, "textDocument/didChange", didChangeTextDocumentParams{
		TextDocument:   versionedTextDocumentIdentifier{URI: valuesURI, Version: 2},
		ContentChanges: []textDocumentContentChangeEvent{{Text: "value: {{ .vars.missing }}"}},
	})
	require.NotEmpty(t, publications[valuesURI].Diagnostics)
	require.NotNil(t, publications[valuesURI].Version)
	assert.Equal(t, int32(2), *publications[valuesURI].Version)
	publications = diagnosticNotification(t, store, "textDocument/didChange", didChangeTextDocumentParams{
		TextDocument:   versionedTextDocumentIdentifier{URI: valuesURI, Version: 1},
		ContentChanges: []textDocumentContentChangeEvent{{Text: "value: true"}},
	})
	require.NotEmpty(t, publications[valuesURI].Diagnostics, "stale changes must not clear a current diagnostic")
	publications = diagnosticNotification(t, store, "textDocument/didClose", didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: valuesURI}})
	assert.Empty(t, publications[valuesURI].Diagnostics)
	assert.Nil(t, publications[valuesURI].Version)
	publications = diagnosticNotification(t, store, "textDocument/didClose", didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: bundleURI}})
	for _, publication := range publications {
		assert.Empty(t, publication.Diagnostics, "closing the last document clears all bundle diagnostics")
	}
}

func newDiagnosticWorkspace(t *testing.T) (*documentStore, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "values"), 0o700))
	bundlePath := filepath.Join(dir, "bundle.uds.hcl")
	bundleText := strings.Replace(validBundle, `source = "${local.repo}/app:${sys.arch}"`, "source = \"${local.repo}/app:${sys.arch}\"\n  values_files = [\"values/app.yaml\"]", 1)
	writeDiagnosticFile(t, bundlePath, bundleText)
	writeDiagnosticFile(t, filepath.Join(dir, "defaults.uds.hcl"), `variables = { replicas = 2 }`)
	writeDiagnosticFile(t, filepath.Join(dir, "config.uds.hcl"), `variables = { replicas = 3 }`)
	valuesPath := filepath.Join(dir, "values/app.yaml")
	writeDiagnosticFile(t, valuesPath, "replicas: {{ .vars.replicas }}\n")
	store := newDocumentStore()
	bundleURI, valuesURI := valuesFileURI(bundlePath), valuesFileURI(valuesPath)
	store.documents[bundleURI] = document{URI: bundleURI, Version: 1, Text: bundleText}
	store.documents[valuesURI] = document{URI: valuesURI, Version: 1, Text: "replicas: {{ .vars.replicas }}\n"}
	return store, dir, bundleURI, valuesURI
}

func writeDiagnosticFile(t *testing.T, filename, text string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filename, []byte(text), 0o600))
}

func diagnosticNotification(t *testing.T, store *documentStore, method string, params any) map[string]publishDiagnosticsParams {
	t.Helper()
	encoded, err := json.Marshal(params)
	require.NoError(t, err)
	var output bytes.Buffer
	_, _, err = handleMessage(t.Context(), bufio.NewWriter(&output), message{JSONRPC: jsonRPCVersion, Method: method, Params: encoded}, stateInitialized, store, iostreams.IOStreams{})
	require.NoError(t, err)
	publications := make(map[string]publishDiagnosticsParams)
	reader := bufio.NewReader(&output)
	for {
		payload, err := readMessage(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		var msg message
		require.NoError(t, json.Unmarshal(payload, &msg))
		require.Equal(t, "textDocument/publishDiagnostics", msg.Method)
		var publication publishDiagnosticsParams
		require.NoError(t, json.Unmarshal(msg.Params, &publication))
		require.NotNil(t, publication.Diagnostics, "diagnostics must encode as an array")
		publications[publication.URI] = publication
	}
	return publications
}
