package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/hashicorp/hcl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validBundle = `uds {
  bundle_api_version = "uds.dev/v1alpha1"
}
locals {
  repo = "oci://example.com/packages"
}
metadata {
  name = "example"
}
package "app" {
  source = "${local.repo}/app:${sys.arch}"
}
`

func TestServeBundleDiagnostics(t *testing.T) {
	for _, versionSupport := range []bool{false, true} {
		t.Run(map[bool]string{false: "without versions", true: "with versions"}[versionSupport], func(t *testing.T) {
			uri := "file:///workspace/bundle.uds.hcl"
			init := map[string]any{}
			if versionSupport {
				init["capabilities"] = map[string]any{
					"textDocument": map[string]any{
						"publishDiagnostics": map[string]any{"versionSupport": true},
					},
				}
			}
			input := framedMessages(t,
				encodeMessage(t, "initialize", 1, init),
				encodeMessage(t, "initialized", nil, map[string]any{}),
				encodeMessage(t, "textDocument/didOpen", nil, didOpenTextDocumentParams{
					TextDocument: textDocumentItem{URI: uri, LanguageID: "hcl", Version: 1, Text: "metadata {"},
				}),
				encodeMessage(t, "textDocument/didChange", nil, didChangeTextDocumentParams{
					TextDocument:   versionedTextDocumentIdentifier{URI: uri, Version: 2},
					ContentChanges: []textDocumentContentChangeEvent{{Text: validBundle}},
				}),
				// An older edit must not replace the current valid buffer.
				encodeMessage(t, "textDocument/didChange", nil, didChangeTextDocumentParams{
					TextDocument:   versionedTextDocumentIdentifier{URI: uri, Version: 1},
					ContentChanges: []textDocumentContentChangeEvent{{Text: "metadata {"}},
				}),
				encodeMessage(t, "textDocument/didSave", nil, didSaveTextDocumentParams{
					TextDocument: textDocumentIdentifier{URI: uri},
				}),
				encodeMessage(t, "textDocument/didClose", nil, didCloseTextDocumentParams{
					TextDocument: textDocumentIdentifier{URI: uri},
				}),
				encodeMessage(t, "shutdown", 2, nil),
				encodeMessage(t, "exit", nil, nil),
			)
			var output bytes.Buffer
			require.NoError(t, Serve(t.Context(), input, &output, iostreams.IOStreams{}))

			reader := bufio.NewReader(&output)
			var publications []publishDiagnosticsParams
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
				assert.Nil(t, msg.ID, "diagnostics must be notifications")
				var params publishDiagnosticsParams
				require.NoError(t, json.Unmarshal(msg.Params, &params))
				publications = append(publications, params)
			}
			require.Len(t, publications, 5)
			require.NotEmpty(t, publications[0].Diagnostics)
			assert.Equal(t, 1, publications[0].Diagnostics[0].Severity)
			assert.Equal(t, "uds-lsp", publications[0].Diagnostics[0].Source)
			for i, params := range publications {
				assert.Equal(t, uri, params.URI)
				if i > 0 {
					require.NotNil(t, params.Diagnostics, "empty diagnostics must encode as []")
					assert.Empty(t, params.Diagnostics)
				}
				if versionSupport && i < 4 {
					require.NotNil(t, params.Version)
					assert.Equal(t, int32(min(i+1, 2)), *params.Version)
				} else {
					assert.Nil(t, params.Version)
				}
			}
		})
	}
}

func TestBundleDiagnosticsValidation(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "valid Next bundle", text: validBundle},
		{name: "empty document", want: "required"},
		{name: "syntax", text: "metadata {", want: "Unclosed"},
		{name: "missing source", text: strings.ReplaceAll(validBundle, `source = "${local.repo}/app:${sys.arch}"`, ""), want: `"source"`},
		{name: "unsupported API", text: strings.ReplaceAll(validBundle, "uds.dev/v1alpha1", "uds.dev/unsupported"), want: "unsupported"},
		{name: "unknown local", text: strings.ReplaceAll(validBundle, "local.repo", "local.missing"), want: "Unsupported attribute"},
		{name: "unknown dependency", text: validBundle + "package \"other\" {\n source = \"other\"\n depends_on = [package.missing]\n}\n", want: "unknown"},
		{name: "dependency cycle", text: strings.ReplaceAll(validBundle, `source = "${local.repo}/app:${sys.arch}"`, "source = \"app\"\n depends_on = [package.other]") + "package \"other\" {\n source = \"other\"\n depends_on = [package.app]\n}\n", want: "cycle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, err := url.Parse("file:///workspace/bundle.uds.hcl")
			require.NoError(t, err)
			diags := bundleDiagnostics(t.Context(), document{Text: tt.text}, uri)
			if tt.want == "" {
				assert.Empty(t, diags)
				return
			}
			require.NotEmpty(t, diags)
			var messages []string
			for _, d := range diags {
				messages = append(messages, d.Message)
			}
			assert.Contains(t, strings.Join(messages, "\n"), tt.want)
		})
	}
}

func TestBundleDiagnosticsUsesBufferAndSourceDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "name.txt"), []byte("my-bundle"), 0o600))
	filename := filepath.Join(dir, "bundle.uds.hcl")
	// Invalid disk content must not override the valid unsaved editor content.
	require.NoError(t, os.WriteFile(filename, []byte("metadata {"), 0o600))
	uri := &url.URL{Scheme: "file", Path: filepath.ToSlash(filename)}
	text := strings.ReplaceAll(validBundle, `name = "example"`, `name = file("name.txt")`)
	assert.Empty(t, bundleDiagnostics(t.Context(), document{Text: text}, uri))

	// Non-file documents have no local base directory for file().
	uri = &url.URL{Scheme: "untitled", Path: "bundle.uds.hcl"}
	diags := bundleDiagnostics(t.Context(), document{Text: text}, uri)
	require.NotEmpty(t, diags)
	assert.Contains(t, diags[0].Message, "file")
}

func TestDiagnosticUTF16Range(t *testing.T) {
	text := "# comment\r\nmetadata { name = \"😀é\" bad = 1 }\n"
	start := strings.Index(text, "bad")
	err := hcl.Diagnostics{&hcl.Diagnostic{
		Severity: hcl.DiagError, Summary: "Invalid attribute",
		Subject: &hcl.Range{Start: hcl.Pos{Byte: start}, End: hcl.Pos{Byte: start + 3}},
	}}
	diags := diagnosticsForError(err, text)
	require.Len(t, diags, 1)
	assert.Equal(t, documentRange{
		Start: position{Line: 1, Character: 24},
		End:   position{Line: 1, Character: 27},
	}, diags[0].Range)
}

func TestServeInvalidInitializeParameters(t *testing.T) {
	input := framedMessages(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":false}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"capabilities":{"unknownFutureCapability":true}}}`,
	)
	var output bytes.Buffer
	require.NoError(t, Serve(t.Context(), input, &output, iostreams.IOStreams{}))
	responses := decodeResponses(t, &output)
	require.Len(t, responses, 2)
	require.NotNil(t, responses[0].Error)
	assert.Equal(t, codeInvalidParams, responses[0].Error.Code)
	assert.Nil(t, responses[1].Error)
}

func encodeMessage(t *testing.T, method string, id any, params any) string {
	t.Helper()
	msg := map[string]any{"jsonrpc": jsonRPCVersion, "method": method}
	if id != nil {
		msg["id"] = id
	}
	if params != nil {
		msg["params"] = params
	}
	encoded, err := json.Marshal(msg)
	require.NoError(t, err)
	return string(encoded)
}
