package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundleCompletionContext(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    []string
		exclude []string
	}{
		{name: "root", text: "|", want: []string{"uds", "metadata", "package", "locals"}, exclude: []string{"source"}},
		{name: "partial root block", text: "met|", want: []string{"metadata", "package"}},
		{name: "metadata", text: "metadata {\n  |\n}\n", want: []string{"name", "description", "version"}, exclude: []string{"source", "package"}},
		{name: "opening brace trigger", text: "metadata {|}\n", want: []string{"name", "version"}},
		{name: "partial attribute", text: "package \"app\" {\n  so|\n}\n", want: []string{"source", "namespace", "values_files", "depends_on", "signature_verification"}, exclude: []string{"name", "metadata"}},
		{name: "unclosed block", text: "package \"app\" {\n  so|", want: []string{"source", "namespace"}},
		{name: "nested block", text: "package \"app\" {\n signature_verification {\n |\n }\n}\n", want: []string{"verify", "public_key", "keyless"}, exclude: []string{"source", "certificate_identity"}},
		{name: "deeply nested", text: "package \"app\" {\n signature_verification {\n keyless {\n |\n }\n }\n}\n", want: []string{"certificate_identity", "insecure_ignore_tlog"}, exclude: []string{"verify", "source"}},
		{name: "existing attributes on both sides", text: "metadata {\n name = \"app\"\n |\n version = \"1\"\n}\n", want: []string{"description"}, exclude: []string{"name", "version"}},
		{name: "existing singleton block", text: "uds {}\n|\n", want: []string{"metadata", "package"}, exclude: []string{"uds"}},
		{name: "repeatable block", text: "package \"app\" {}\n|\n", want: []string{"package"}},
		{name: "existing policy block", text: "package \"app\" {\n signature_verification {}\n |\n}\n", want: []string{"source"}, exclude: []string{"signature_verification"}},
		{name: "unknown body", text: "unknown {\n |\n}\n"},
		{name: "locals", text: "locals {\n |\n}\n"},
		{name: "expression", text: "metadata {\n name = local.|\n}\n"},
		{name: "object expression", text: "metadata {\n name = {\n |\n }\n}\n"},
		{name: "list expression", text: "package \"app\" {\n values_files = [\n |\n ]\n}\n"},
		{name: "quoted string", text: "metadata { name = \"a|b\" }\n"},
		{name: "string interpolation", text: "metadata { name = \"${local.|}\" }\n"},
		{name: "heredoc", text: "metadata {\n name = <<EOT\n |\nEOT\n}\n"},
		{name: "comment", text: "# na|me\n"},
		{name: "block comment", text: "/*\n |\n*/\n"},
		{name: "block label", text: "package \"ap|p\" {}\n"},
		{name: "unicode CRLF", text: "# 😀\r\npackage \"😀app\" { na| }\r\n", want: []string{"namespace", "source"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.text)
			store := newDocumentStore()
			store.completionSnippetSupport = true
			uri := "file:///workspace/bundle.uds.hcl"
			store.documents[uri] = document{URI: uri, Text: text}
			result := store.complete(documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})
			var labels []string
			for _, item := range result.Items {
				labels = append(labels, item.Label)
			}
			if len(tt.want) == 0 {
				assert.Empty(t, labels)
			}
			for _, name := range tt.want {
				assert.Contains(t, labels, name)
			}
			for _, name := range tt.exclude {
				assert.NotContains(t, labels, name)
			}
		})
	}
}

func TestFileSpecificCompletion(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		marked  string
		want    []string
		exclude []string
	}{
		{
			name:    "config root",
			uri:     "file:///workspace/config.uds.hcl",
			marked:  "|",
			want:    []string{"options", "signature_verification", "variables"},
			exclude: []string{"metadata", "package", "locals"},
		},
		{
			name:    "config options",
			uri:     "file:///workspace/config.uds.hcl",
			marked:  "options {\n  |\n}\n",
			want:    []string{"log_level", "architecture", "plain_http", "skip_tls_verify", "tmp_dir", "concurrency"},
			exclude: []string{"variables", "public_key"},
		},
		{
			name:   "config keyless verification",
			uri:    "file:///workspace/config.uds.hcl",
			marked: "signature_verification {\n  keyless {\n    |\n  }\n}\n",
			want:   []string{"certificate_identity", "certificate_identity_regexp", "certificate_oidc_issuer", "certificate_oidc_issuer_regexp", "trusted_root"},
		},
		{
			name:    "defaults root",
			uri:     "file:///workspace/defaults.uds.hcl",
			marked:  "|",
			want:    []string{"variables"},
			exclude: []string{"options", "signature_verification", "metadata"},
		},
		{
			name:   "unknown HCL file",
			uri:    "file:///workspace/other.hcl",
			marked: "|",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.marked)
			store := newDocumentStore()
			store.documents[tt.uri] = document{URI: tt.uri, Text: text}

			result := store.complete(documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: tt.uri},
				Position:     &pos,
			})

			labels := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				labels = append(labels, item.Label)
			}
			for _, label := range tt.want {
				assert.Contains(t, labels, label)
			}
			for _, label := range tt.exclude {
				assert.NotContains(t, labels, label)
			}
			if len(tt.want) == 0 {
				assert.Empty(t, labels)
			}
		})
	}
}

func TestReferenceCompletion(t *testing.T) {
	tests := []struct {
		name     string
		marked   string
		want     []string
		oldText  string
		newText  string
		itemKind int
	}{
		{
			name: "local namespace",
			marked: `locals { verify = true }
package "app" { source = "${loc|}" }
`,
			want:     []string{"local"},
			oldText:  "loc",
			newText:  "local.",
			itemKind: completionItemKindModule,
		},
		{
			name: "package namespace",
			marked: `package "base" { source = "base" }
package "app" {
  source = "app"
  depends_on = [pac|]
}
`,
			want:     []string{"package"},
			oldText:  "pac",
			newText:  "package.",
			itemKind: completionItemKindModule,
		},
		{
			name: "local in interpolation",
			marked: `locals {
  repo = "example.com"
  version = "1.0.0"
}
package "app" { source = "oci://${local.re|po}/app:${local.version}" }
`,
			want:     []string{"repo", "version"},
			oldText:  "repo",
			itemKind: completionItemKindVariable,
		},
		{
			name: "package dependency",
			marked: `package "base" { source = "base" }
package "app" {
  source = "app"
  depends_on = [package.b|ase]
}
`,
			want:     []string{"app", "base"},
			oldText:  "base",
			itemKind: completionItemKindReference,
		},
		{
			name: "not in plain string",
			marked: `locals { repo = "example.com" }
metadata { name = "local.|" }
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.marked)
			uri := "file:///workspace/bundle.uds.hcl"
			store := newDocumentStore()
			store.documents[uri] = document{URI: uri, Text: text}

			result := store.complete(documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri},
				Position:     &pos,
			})

			labels := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				labels = append(labels, item.Label)
				assert.Equal(t, tt.itemKind, item.Kind)
				assert.Equal(t, insertTextFormatPlainText, item.InsertTextFormat)
			}
			if len(tt.want) == 0 {
				assert.Empty(t, labels)
				return
			}
			assert.Equal(t, tt.want, labels)

			item := completionByLabel(t, result, tt.want[0])
			start, ok := byteAtPosition(text, item.TextEdit.Range.Start)
			require.True(t, ok)
			end, ok := byteAtPosition(text, item.TextEdit.Range.End)
			require.True(t, ok)
			assert.Equal(t, tt.oldText, text[start:end])
			wantNewText := item.Label
			if tt.newText != "" {
				wantNewText = tt.newText
			}
			assert.Equal(t, wantNewText, item.TextEdit.NewText)
		})
	}
}

func TestCompletionEdits(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		label  string
		insert string
		format int
	}{
		{name: "replace partial word", text: "package \"app\" {\n so|urce\n}\n", label: "source", insert: `source = "${1}"$0`, format: 2},
		{name: "list placeholder", text: "package \"app\" {\n val|\n}\n", label: "values_files", insert: "values_files = [${1}]$0", format: 2},
		{name: "boolean choice", text: "package \"app\" {\n signature_verification {\n ver|\n }\n}\n", label: "verify", insert: "verify = ${1|true,false|}$0", format: 2},
		{name: "package dependency", text: "package \"app\" {\n dep|\n}\n", label: "depends_on", insert: "depends_on = [package.${1:name}]$0", format: 2},
		{name: "block label and body", text: "pac|", label: "package", insert: "package \"${1:name}\" {\n  $0\n}", format: 2},
		{name: "inline nested block", text: "package \"app\" {|}\n", label: "signature_verification", insert: "\n  signature_verification {\n    $0\n  }\n", format: 2},
		{name: "existing assignment", text: "package \"app\" {\n so|urce = \"keep-me\"\n}\n", label: "source", insert: "source", format: 1},
		{name: "existing block header", text: "meta|data { name = \"keep-me\" }\n", label: "metadata", insert: "metadata", format: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.text)
			store := newDocumentStore()
			store.completionSnippetSupport = true
			uri := "file:///workspace/bundle.uds.hcl"
			store.documents[uri] = document{URI: uri, Text: text}
			result := store.complete(documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})
			item := completionByLabel(t, result, tt.label)
			assert.Equal(t, tt.insert, item.TextEdit.NewText)
			assert.Equal(t, tt.format, item.InsertTextFormat)
			start, ok := byteAtPosition(text, item.TextEdit.Range.Start)
			require.True(t, ok)
			end, ok := byteAtPosition(text, item.TextEdit.Range.End)
			require.True(t, ok)
			assert.True(t, strings.HasPrefix(tt.label, text[start:end]), "edit should replace the identifier, not surrounding HCL")
			if tt.format == 1 {
				edited := text[:start] + item.TextEdit.NewText + text[end:]
				assert.Contains(t, edited, `"keep-me"`)
			}
		})
	}
}

func TestServeCompletion(t *testing.T) {
	for _, snippets := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain text", true: "snippets"}[snippets], func(t *testing.T) {
			uri := "file:///workspace/bundle.uds.hcl"
			params := documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &position{Line: 1, Character: 2}}
			input := framedMessages(t,
				encodeMessage(t, "textDocument/completion", "before", params),
				encodeMessage(t, "initialize", 1, map[string]any{"capabilities": map[string]any{
					"textDocument": map[string]any{"completion": map[string]any{"completionItem": map[string]any{
						"snippetSupport": snippets, "documentationFormat": []string{"markdown", "plaintext"},
					}}},
				}}),
				encodeMessage(t, "initialized", nil, map[string]any{}),
				encodeMessage(t, "textDocument/completion", "unopened", params),
				encodeMessage(t, "textDocument/didOpen", nil, didOpenTextDocumentParams{TextDocument: textDocumentItem{
					URI: uri, LanguageID: "hcl", Version: 1, Text: "metadata {\n  \n}\n",
				}}),
				encodeMessage(t, "textDocument/completion", "open", params),
				encodeMessage(t, "textDocument/completion", nil, params),
				encodeMessage(t, "textDocument/didChange", nil, didChangeTextDocumentParams{
					TextDocument:   versionedTextDocumentIdentifier{URI: uri, Version: 2},
					ContentChanges: []textDocumentContentChangeEvent{{Text: "metadata {\n  name = \"app\"\n  \n}\n"}},
				}),
				encodeMessage(t, "textDocument/completion", "edited", documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &position{Line: 2}}),
				encodeMessage(t, "textDocument/didClose", nil, didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: uri}}),
				encodeMessage(t, "textDocument/completion", "closed", params),
				encodeMessage(t, "textDocument/completion", "invalid", map[string]any{}),
				encodeMessage(t, "shutdown", 2, nil),
				encodeMessage(t, "textDocument/completion", "after", params),
				encodeMessage(t, "exit", nil, nil),
			)
			var output bytes.Buffer
			require.NoError(t, Serve(t.Context(), input, &output, iostreams.IOStreams{}))
			reader := bufio.NewReader(&output)
			responses := make(map[string]response)
			for {
				payload, err := readMessage(reader)
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)
				var msg response
				require.NoError(t, json.Unmarshal(payload, &msg))
				if msg.ID != nil {
					responses[string(msg.ID)] = msg
				}
			}
			require.Len(t, responses, 9)
			require.NotNil(t, responses[`"before"`].Error)
			assert.Equal(t, codeServerNotInitialized, responses[`"before"`].Error.Code)
			require.NotNil(t, responses[`"invalid"`].Error)
			assert.Equal(t, codeInvalidParams, responses[`"invalid"`].Error.Code)
			require.NotNil(t, responses[`"after"`].Error)
			assert.Equal(t, codeInvalidRequest, responses[`"after"`].Error.Code)
			for _, id := range []string{`"unopened"`, `"closed"`} {
				var result completionList
				require.NoError(t, json.Unmarshal(responses[id].Result, &result))
				assert.Empty(t, result.Items)
				require.NotNil(t, result.Items)
			}
			var result completionList
			require.NoError(t, json.Unmarshal(responses[`"open"`].Result, &result))
			item := completionByLabel(t, result, "name")
			assert.Equal(t, "markdown", item.Documentation.Kind)
			if snippets {
				assert.Equal(t, 2, item.InsertTextFormat)
				assert.Equal(t, `name = "${1}"$0`, item.TextEdit.NewText)
			} else {
				assert.Equal(t, 1, item.InsertTextFormat)
				assert.Equal(t, `name = ""`, item.TextEdit.NewText)
			}
			require.NoError(t, json.Unmarshal(responses[`"edited"`].Result, &result))
			for _, item := range result.Items {
				assert.NotEqual(t, "name", item.Label)
			}
		})
	}
}

func completionByLabel(t *testing.T, result completionList, label string) completionItem {
	t.Helper()
	for _, item := range result.Items {
		if item.Label == label {
			return item
		}
	}
	require.FailNow(t, "missing completion", "label: %s; result: %+v", label, result)
	return completionItem{}
}
