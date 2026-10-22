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

func TestBundleHover(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "required attribute", text: "package \"app\" {\n  so|urce = \"app\"\n}\n", want: []string{"```hcl\nsource = \"...\"\n```", "**Required**", "Type: `string`", "*Parser schema*"}},
		{name: "optional list", text: "package \"app\" {\n  values_|files = []\n}\n", want: []string{"```hcl\nvalues_files = []\n```", "`list(string)`", "**Optional**"}},
		{name: "optional pointer", text: "package \"app\" {\n signature_verification {\n ver|ify = false\n }\n}\n", want: []string{"```hcl\nverify = false\n```", "`bool`", "**Optional**"}},
		{name: "nested block", text: "package \"app\" {\n signature_verification {\n key|less {}\n }\n}\n", want: []string{"`keyless`", "`certificate_identity`", "`insecure_ignore_tlog`"}},
		{name: "block overview", text: "pack|age \"app\" {\n source = \"app\"\n}\n", want: []string{"**`package \"<name>\"`**", "repeatable", "| Field | Type | Required |", "| `source` | `string` | Yes |", "| `signature_verification` | `block` | No |"}},
		{name: "package label", text: "package \"ap|p\" {\n source = \"app\"\n}\n", want: []string{"`package \"<name>\"`"}},
		{name: "metadata field", text: "metadata { na|me = \"example\" }\n", want: []string{"name = \"...\"", "**Required**"}},
		{name: "semantic errors elsewhere", text: "metadata { na|me = local.missing }\n", want: []string{"name = \"...\"", "`string`"}},
		{name: "unevaluated file function", text: "metadata { na|me = file(\"/missing/hover-must-not-read-this\") }\n", want: []string{"name = \"...\"", "`string`"}},
		{name: "incomplete file", text: "package \"app\" {\n so|urce = \"app\"\n", want: []string{"source = \"...\"", "`string`"}},
		{name: "unicode and CRLF", text: "# 😀é\r\npackage \"😀app\" { so|urce = \"app\" }\r\n", want: []string{"source = \"...\""}},
		{name: "unknown attribute", text: "metadata { un|known = 1 }\n"},
		{name: "unknown block", text: "unknown { na|me = \"app\" }\n"},
		{name: "custom dependency", text: "package \"app\" {\n depends_|on = []\n}\n"},
		{name: "custom locals", text: "locals { so|urce = \"app\" }\n"},
		{name: "value", text: "metadata { name = \"ex|ample\" }\n"},
		{name: "comment", text: "# so|urce\n"},
		{name: "whitespace", text: "|\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.text)
			store := newDocumentStore()
			store.hoverContentFormat = "markdown"
			uri := "file:///workspace/bundle.uds.hcl"
			store.documents[uri] = document{URI: uri, Text: text}
			result := store.hover(hoverParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})
			if len(tt.want) == 0 {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			assert.Equal(t, "markdown", result.Contents.Kind)
			for _, expected := range tt.want {
				assert.Contains(t, result.Contents.Value, expected)
			}
			start, ok := byteAtPosition(text, result.Range.Start)
			require.True(t, ok)
			end, ok := byteAtPosition(text, result.Range.End)
			require.True(t, ok)
			require.Less(t, start, end)
			cursor, ok := byteAtPosition(text, pos)
			require.True(t, ok)
			assert.GreaterOrEqual(t, cursor, start)
			assert.Less(t, cursor, end)
		})
	}
}

func TestServeHoverLifecycle(t *testing.T) {
	for _, formats := range [][]string{nil, {"markdown", "plaintext"}, {"plaintext", "markdown"}} {
		t.Run(strings.Join(formats, ","), func(t *testing.T) {
			uri := "file:///workspace/bundle.uds.hcl"
			text := "metadata { name = \"app\" }\n"
			params := hoverParams{
				TextDocument: textDocumentIdentifier{URI: uri},
				Position:     &position{Line: 0, Character: 12},
			}
			input := framedMessages(t,
				encodeMessage(t, "textDocument/hover", "before", params),
				encodeMessage(t, "initialize", 1, map[string]any{"capabilities": map[string]any{
					"textDocument": map[string]any{"hover": map[string]any{"contentFormat": formats}},
				}}),
				encodeMessage(t, "initialized", nil, map[string]any{}),
				encodeMessage(t, "textDocument/hover", "unopened", params),
				encodeMessage(t, "textDocument/didOpen", nil, didOpenTextDocumentParams{
					TextDocument: textDocumentItem{URI: uri, LanguageID: "hcl", Version: 1, Text: text},
				}),
				encodeMessage(t, "textDocument/hover", "open", params),
				encodeMessage(t, "textDocument/hover", nil, params),
				encodeMessage(t, "textDocument/didChange", nil, didChangeTextDocumentParams{
					TextDocument:   versionedTextDocumentIdentifier{URI: uri, Version: 2},
					ContentChanges: []textDocumentContentChangeEvent{{Text: "# no attributes\n"}},
				}),
				encodeMessage(t, "textDocument/hover", "edited", params),
				encodeMessage(t, "textDocument/didClose", nil, didCloseTextDocumentParams{TextDocument: textDocumentIdentifier{URI: uri}}),
				encodeMessage(t, "textDocument/hover", "closed", params),
				encodeMessage(t, "textDocument/hover", "invalid", map[string]any{"textDocument": map[string]any{"uri": uri}}),
				encodeMessage(t, "shutdown", 2, nil),
				encodeMessage(t, "textDocument/hover", "after", params),
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
			for _, id := range []string{`"unopened"`, `"edited"`, `"closed"`} {
				assert.JSONEq(t, "null", string(responses[id].Result))
			}
			var result hoverResult
			require.NoError(t, json.Unmarshal(responses[`"open"`].Result, &result))
			wantFormat := "plaintext"
			if len(formats) > 0 {
				wantFormat = formats[0]
			}
			assert.Equal(t, wantFormat, result.Contents.Kind)
			assert.Contains(t, result.Contents.Value, "name")
			assert.Contains(t, result.Contents.Value, "string")
			if wantFormat == "markdown" {
				assert.Contains(t, result.Contents.Value, "```hcl\nname = \"...\"\n```")
				assert.Contains(t, result.Contents.Value, "**Required**")
			} else {
				assert.Contains(t, result.Contents.Value, "Required by parser: yes")
				assert.NotContains(t, result.Contents.Value, "```")
				assert.NotContains(t, result.Contents.Value, "**")
			}
			require.NotNil(t, responses[`"invalid"`].Error)
			assert.Equal(t, codeInvalidParams, responses[`"invalid"`].Error.Code)
			require.NotNil(t, responses[`"after"`].Error)
			assert.Equal(t, codeInvalidRequest, responses[`"after"`].Error.Code)
		})
	}
}

func TestByteAtPosition(t *testing.T) {
	text := "é😀x\r\nsecond\n"
	tests := []struct {
		pos    position
		offset int
		valid  bool
	}{
		{pos: position{}, offset: 0, valid: true},
		{pos: position{Character: 1}, offset: 2, valid: true},
		{pos: position{Character: 2}, valid: false},
		{pos: position{Character: 3}, offset: 6, valid: true},
		{pos: position{Character: 4}, offset: 7, valid: true},
		{pos: position{Character: 100}, offset: 7, valid: true},
		{pos: position{Line: 1}, offset: 9, valid: true},
		{pos: position{Line: 2}, offset: len(text), valid: true},
		{pos: position{Line: 3}, valid: false},
		{pos: position{Line: -1}, valid: false},
	}
	for _, tt := range tests {
		offset, valid := byteAtPosition(text, tt.pos)
		assert.Equal(t, tt.valid, valid, "position: %+v", tt.pos)
		if valid {
			assert.Equal(t, tt.offset, offset, "position: %+v", tt.pos)
		}
	}
}

func hoverPosition(t *testing.T, marked string) (string, position) {
	t.Helper()
	offset := strings.IndexByte(marked, '|')
	require.NotEqual(t, -1, offset)
	text := marked[:offset] + marked[offset+1:]
	return text, positionAtByte(text, offset)
}
