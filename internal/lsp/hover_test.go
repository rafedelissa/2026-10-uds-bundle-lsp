package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
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

func TestBundleHover(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "required attribute", text: "package \"app\" {\n  so|urce = \"app\"\n}\n", want: []string{"```hcl\nsource = \"...\"\n```", "**Required**", "Type: `string`", "*Parser schema*"}},
		{name: "optional list", text: "package \"app\" {\n  values_|files = []\n}\n", want: []string{"```hcl\nvalues_files = []\n```", "`list(string)`", "**Optional**"}},
		{name: "optional pointer", text: "package \"app\" {\n signature_verification {\n ver|ify = false\n }\n}\n", want: []string{"```hcl\nverify = false\n```", "`bool`", "**Optional**"}},
		{name: "nested block", text: "package \"app\" {\n signature_verification {\n key|less {}\n }\n}\n", want: []string{"`keyless`", "certificate_identity", "insecure_ignore_tlog"}},
		{name: "block overview", text: "pack|age \"app\" {\n source = \"app\"\n}\n", want: []string{"**`package \"<name>\"`**", "repeatable", "| Field", "| source", "| signature_verification"}},
		{name: "package label", text: "package \"ap|p\" {\n source = \"app\"\n}\n", want: []string{"`package \"<name>\"`"}},
		{name: "metadata field", text: "metadata { na|me = \"example\" }\n", want: []string{"name = \"...\"", "**Required**"}},
		{name: "semantic errors elsewhere", text: "metadata { na|me = local.missing }\n", want: []string{"name = \"...\"", "`string`"}},
		{name: "unevaluated file function", text: "metadata { na|me = file(\"/missing/hover-must-not-read-this\") }\n", want: []string{"name = \"...\"", "`string`"}},
		{name: "incomplete file", text: "package \"app\" {\n so|urce = \"app\"\n", want: []string{"source = \"...\"", "`string`"}},
		{name: "unicode and CRLF", text: "# 😀é\r\npackage \"😀app\" { so|urce = \"app\" }\r\n", want: []string{"source = \"...\""}},
		{name: "unknown attribute", text: "metadata { un|known = 1 }\n"},
		{name: "unknown block", text: "unknown { na|me = \"app\" }\n"},
		{name: "custom dependency", text: "package \"app\" {\n depends_|on = []\n}\n", want: []string{"depends_on = []", "`list(package reference)`", "**Optional**"}},
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
			result := store.hover(t.Context(), documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})
			if len(tt.want) == 0 {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			assert.Equal(t, "markdown", result.Contents.Kind)
			for _, expected := range tt.want {
				assert.Contains(t, result.Contents.Value, expected)
			}
			assertHoverRangeContains(t, text, result.Range, pos)
		})
	}
}

func TestMarkdownFieldTableAlignment(t *testing.T) {
	table := markdownFieldTable([]bundle.DefinitionField{
		{Name: "source", Type: "string", Required: true},
		{Name: "signature_verification", Type: "block"},
		{Name: "depends_on", Type: "list(package reference)"},
	})
	lines := strings.Split(table, "\n")
	require.Len(t, lines, 5)
	wantColumns := pipeColumns(lines[0])
	for _, line := range lines[1:] {
		assert.Equal(t, wantColumns, pipeColumns(line), "%q", line)
	}
}

func pipeColumns(line string) []int {
	var columns []int
	for i, r := range line {
		if r == '|' {
			columns = append(columns, i)
		}
	}
	return columns
}

func TestFileSpecificHover(t *testing.T) {
	tests := []struct {
		name   string
		uri    string
		marked string
		want   []string
	}{
		{
			name:   "config option",
			uri:    "file:///workspace/config.uds.hcl",
			marked: "options { concur|rency = 4 }\n",
			want:   []string{"concurrency = 0", "`number`", "**Optional**", "Default: `10`", "Zero uses the default"},
		},
		{
			name:   "log level variants",
			uri:    "file:///workspace/config.uds.hcl",
			marked: "options { log_|level = \"info\" }\n",
			want:   []string{"logging verbosity", "Default: `info`", "Supported values", "`debug`", "`error`", "`info`", "`warn`", "`warning`"},
		},
		{
			name:   "architecture variants",
			uri:    "file:///workspace/config.uds.hcl",
			marked: "options { |architecture = \"amd64\" }\n",
			want:   []string{"Supported values", "`amd64`", "`arm64`"},
		},
		{
			name:   "config verification",
			uri:    "file:///workspace/config.uds.hcl",
			marked: "signature_verification { public_|key = \"key\" }\n",
			want:   []string{"public_key = \"...\"", "`string`"},
		},
		{
			name:   "defaults variables",
			uri:    "file:///workspace/defaults.uds.hcl",
			marked: "vari|ables = {}\n",
			want:   []string{"variables = {}", "`object`"},
		},
		{
			name:   "bundle field excluded from config",
			uri:    "file:///workspace/config.uds.hcl",
			marked: "metadata { na|me = \"app\" }\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.marked)
			store := newDocumentStore()
			store.hoverContentFormat = "markdown"
			store.documents[tt.uri] = document{URI: tt.uri, Text: text}

			result := store.hover(t.Context(), documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: tt.uri},
				Position:     &pos,
			})
			if len(tt.want) == 0 {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			for _, want := range tt.want {
				assert.Contains(t, result.Contents.Value, want)
			}
		})
	}
}

func TestHCLExpressionHover(t *testing.T) {
	tests := []struct {
		name   string
		marked string
		want   []string
	}{
		{
			name: "local reference",
			marked: `locals { repo = "example.com" }
metadata { name = local.re|po }
`,
			want: []string{"`local.repo`", `Defined as`, `"example.com"`},
		},
		{
			name: "local namespace range",
			marked: `locals { repo = "example.com" }
metadata { name = loc|al.repo }
`,
			want: []string{"`local.repo`"},
		},
		{
			name: "package reference",
			marked: `package "base" { source = "oci://example/base:1" }
package "app" {
  source = "app"
  depends_on = [package.|base]
}
`,
			want: []string{"`package.base`", "oci://example/base:1"},
		},
		{
			name:   "system architecture",
			marked: `metadata { name = sys.ar|ch }`,
			want:   []string{"`sys.arch`", "arm64"},
		},
		{
			name:   "file function",
			marked: `metadata { name = fi|le("name.txt") }`,
			want:   []string{"file(path string) string", "name.txt"},
		},
		{
			name:   "OCI package source",
			marked: `package "app" { source = "oci://exam|ple/app:1" }`,
			want:   []string{"Package source", "OCI source", "oci://example/app:1"},
		},
		{
			name: "values file path",
			marked: `uds { bundle_api_version = "uds.dev/v1alpha1" }
metadata { name = "example" }
package "app" {
  source = "app"
  values_files = ["values/app.ya|ml"]
}`,
			want: []string{"Values file", "values/app.yaml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.marked)
			uri := "file:///workspace/bundle.uds.hcl"
			store := newDocumentStore()
			store.hoverContentFormat = "markdown"
			store.documents[uri] = document{URI: uri, Text: text}
			store.documents["file:///workspace/config.uds.hcl"] = document{URI: "file:///workspace/config.uds.hcl", Text: `options { architecture = "arm64" }`}
			store.documents["file:///workspace/values/app.yaml"] = document{URI: "file:///workspace/values/app.yaml", Text: "value: true\n"}

			result := store.hover(t.Context(), documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos,
			})

			require.NotNil(t, result)
			for _, want := range tt.want {
				assert.Contains(t, result.Contents.Value, want)
			}
			assertHoverRangeContains(t, text, result.Range, pos)
		})
	}
}

func TestSystemArchitectureHoverEvaluatesFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "arch.txt"), []byte("test-arch"), 0o600))
	bundleURI := localFileURI(filepath.Join(dir, bundle.BundleFileName))
	configURI := localFileURI(filepath.Join(dir, bundle.BundleConfigFileName))
	text, pos := hoverPosition(t, `metadata { name = sys.ar|ch }`)
	store := newDocumentStore()
	store.hoverContentFormat = "markdown"
	store.documents[bundleURI] = document{URI: bundleURI, Text: text}
	store.documents[configURI] = document{URI: configURI, Text: `options { architecture = file("arch.txt") }`}

	result := store.hover(t.Context(), documentPositionParams{
		TextDocument: textDocumentIdentifier{URI: bundleURI}, Position: &pos,
	})

	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "test-arch")
}

func TestMarkdownCodeSpanExpandsDelimiter(t *testing.T) {
	assert.Equal(t, "``key`name``", markdownCodeSpan("key`name"))
	assert.Equal(t, "`` `edge` ``", markdownCodeSpan("`edge`"))
}

func TestTemplateQuotedHoverRange(t *testing.T) {
	for _, tt := range []struct {
		text string
		at   string
		want string
	}{
		{text: `{{ index .vars "quote\"key" }}`, at: "key", want: `quote\"key`},
		{text: "{{ index .vars `` }}", at: "``", want: "``"},
	} {
		offset := strings.Index(tt.text, tt.at)
		hoverRange, ok := templateQuotedHoverRange(tt.text, offset)
		require.True(t, ok)
		start, ok := byteAtPosition(tt.text, hoverRange.Start)
		require.True(t, ok)
		end, ok := byteAtPosition(tt.text, hoverRange.End)
		require.True(t, ok)
		assert.Equal(t, tt.want, tt.text[start:end])
	}
}

func TestMarkdownHCLBlockExpandsFence(t *testing.T) {
	assert.Equal(t, "````hcl\n\"```\"\n````", markdownHCLBlock("\"```\""))
}

func TestHCLExpressionPreservesBackticks(t *testing.T) {
	for _, tt := range []struct {
		format string
		want   string
	}{
		{format: "plaintext", want: "\"`example`\""},
		{format: "markdown", want: "```hcl\n\"`example`\"\n```"},
	} {
		t.Run(tt.format, func(t *testing.T) {
			text, pos := hoverPosition(t, "locals { repo = \"`example`\" }\nmetadata { name = local.re|po }\n")
			uri := "file:///workspace/bundle.uds.hcl"
			store := newDocumentStore()
			store.hoverContentFormat = tt.format
			store.documents[uri] = document{URI: uri, Text: text}

			result := store.hover(t.Context(), documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos,
			})

			require.NotNil(t, result)
			assert.Contains(t, result.Contents.Value, tt.want)
		})
	}
}

func TestHCLVariableHoverComputedValues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		marked string
		want   string
	}{
		{name: "file type", marked: `variables = { tok|en = file("token.txt") }`, want: "Type: `string`"},
		{name: "adaptive fence", marked: "variables = { scr|ipt = \"```\" }", want: "````hcl\n\"```\"\n````"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.marked)
			uri := "file:///workspace/config.uds.hcl"
			store := newDocumentStore()
			store.hoverContentFormat = "markdown"
			store.documents[uri] = document{URI: uri, Text: text}

			result := store.hover(t.Context(), documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos,
			})

			require.NotNil(t, result)
			assert.Contains(t, result.Contents.Value, tt.want)
		})
	}
}

func TestHCLVariableHover(t *testing.T) {
	tests := []struct {
		name     string
		values   string
		filename string
		key      string
		want     []string
	}{
		{
			name: "config override", values: `value: {{ .vars.domain }}`, filename: "config.uds.hcl", key: "domain",
			want: []string{"`.vars.domain`", "Type: `string`", `"example.com"`, "Overrides the default value", "values/app.yaml"},
		},
		{
			name: "default", values: `value: {{ .vars.replicas }}`, filename: "defaults.uds.hcl", key: "replicas",
			want: []string{"`.vars.replicas`", "Type: `number`", "2", "values/app.yaml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := newValuesCompletionStore(t, tt.values)
			var uri string
			for candidate := range store.documents {
				if strings.HasSuffix(candidate, tt.filename) {
					uri = candidate
					break
				}
			}
			require.NotEmpty(t, uri)
			doc := store.documents[uri]
			marked := strings.Replace(doc.Text, tt.key+" =", tt.key[:2]+"|"+tt.key[2:]+" =", 1)
			text, pos := hoverPosition(t, marked)
			doc.Text = text
			store.documents[uri] = doc
			store.hoverContentFormat = "markdown"
			store.workspaceDiagnostics(t.Context())

			result := store.hover(t.Context(), documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos,
			})

			require.NotNil(t, result)
			for _, want := range tt.want {
				assert.Contains(t, result.Contents.Value, want)
			}
		})
	}
}

func TestHCLVariableHoverDetectsAncestorOverride(t *testing.T) {
	store, _ := newValuesCompletionStore(t, `value: {{ .vars.features.level }}`)
	var configURI, defaultsURI string
	for uri := range store.documents {
		switch {
		case strings.HasSuffix(uri, bundle.BundleConfigFileName):
			configURI = uri
		case strings.HasSuffix(uri, bundle.BundleDefaultsFileName):
			defaultsURI = uri
		}
	}
	require.NotEmpty(t, configURI)
	require.NotEmpty(t, defaultsURI)
	config := store.documents[configURI]
	config.Text = `variables = { features = "disabled" }`
	store.documents[configURI] = config
	defaults := store.documents[defaultsURI]
	marked := strings.Replace(defaults.Text, "level =", "|level =", 1)
	text, pos := hoverPosition(t, marked)
	defaults.Text = text
	store.documents[defaultsURI] = defaults
	store.hoverContentFormat = "markdown"

	result := store.hover(t.Context(), documentPositionParams{
		TextDocument: textDocumentIdentifier{URI: defaultsURI}, Position: &pos,
	})

	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Overridden by config.")
}

func TestValuesVariableHover(t *testing.T) {
	tests := []struct {
		name      string
		marked    string
		want      []string
		wantRange string
	}{
		{
			name: "config override", marked: `domain: {{ .vars.dom|ain }}`,
			want: []string{"`.vars.domain`", "config.uds.hcl", "defaults.uds.hcl", "Config overrides the default value."}, wantRange: "domain",
		},
		{
			name: "nested variable", marked: `level: {{ .vars.features.le|vel }}`,
			want: []string{"`.vars.features.level`", "config.uds.hcl", "defaults.uds.hcl"}, wantRange: "level",
		},
		{
			name: "index key", marked: `managedBy: {{ index .vars.annotations "app.kubernetes.io/managed-|by" }}`,
			want: []string{"`index .vars.annotations \"app.kubernetes.io/managed-by\"`", "defaults.uds.hcl"}, wantRange: "app.kubernetes.io/managed-by",
		},
		{
			name: "hyphenated index key", marked: `value: {{ index .vars "invalid-|key" }}`,
			want: []string{"`index .vars \"invalid-key\"`", "defaults.uds.hcl"}, wantRange: "invalid-key",
		},
		{
			name: "alias root", marked: `{{ $cfg := .vars.features }}value: {{ $c|fg }}`,
			want: []string{"`.vars.features`", "config.uds.hcl", "defaults.uds.hcl"}, wantRange: "$cfg",
		},
		{
			name: "unicode field", marked: `value: {{ .vars.区|域 }}`,
			want: []string{"`.vars.区域`", "defaults.uds.hcl"}, wantRange: "区域",
		},
		{
			name: "field first character", marked: `value: {{ .vars.|features }}`,
			want: []string{"`.vars.features`", "config.uds.hcl", "defaults.uds.hcl"}, wantRange: "features",
		},
		{
			name: "alias matching leaf", marked: `{{ $features := .vars.features }}value: {{ $fea|tures }}`,
			want: []string{"`.vars.features`", "config.uds.hcl", "defaults.uds.hcl"}, wantRange: "$features",
		},
		{
			name: "alias dollar", marked: `{{ $cfg := .vars.features }}value: {{ |$cfg }}`,
			want: []string{"`.vars.features`", "config.uds.hcl", "defaults.uds.hcl"}, wantRange: "$cfg",
		},
		{
			name: "scoped dot", marked: `{{ with .vars.features }}value: {{ .| }}{{ end }}`,
			want: []string{"`.vars.features`", "config.uds.hcl", "defaults.uds.hcl"}, wantRange: ".",
		},
		{name: "unknown variable", marked: `value: {{ .vars.miss|ing }}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, uri := newValuesCompletionStore(t, tt.marked)
			text, pos := hoverPosition(t, store.documents[uri].Text)
			doc := store.documents[uri]
			doc.Text = text
			store.documents[uri] = doc
			store.hoverContentFormat = "markdown"

			result := store.hover(t.Context(), documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos,
			})
			if len(tt.want) == 0 {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			for _, want := range tt.want {
				assert.Contains(t, result.Contents.Value, want)
			}
			start, ok := byteAtPosition(text, result.Range.Start)
			require.True(t, ok)
			end, ok := byteAtPosition(text, result.Range.End)
			require.True(t, ok)
			assert.Equal(t, tt.wantRange, text[start:end])
		})
	}
}

func TestServeHoverLifecycle(t *testing.T) {
	for _, formats := range [][]string{nil, {"markdown", "plaintext"}, {"plaintext", "markdown"}} {
		t.Run(strings.Join(formats, ","), func(t *testing.T) {
			uri := "file:///workspace/bundle.uds.hcl"
			text := "metadata { name = \"app\" }\n"
			params := documentPositionParams{
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

func assertHoverRangeContains(t *testing.T, text string, hoverRange documentRange, pos position) {
	t.Helper()
	start, ok := byteAtPosition(text, hoverRange.Start)
	require.True(t, ok)
	end, ok := byteAtPosition(text, hoverRange.End)
	require.True(t, ok)
	require.Less(t, start, end)
	cursor, ok := byteAtPosition(text, pos)
	require.True(t, ok)
	assert.LessOrEqual(t, start, cursor)
	assert.Greater(t, end, cursor)
}

func hoverPosition(t *testing.T, marked string) (string, position) {
	t.Helper()
	offset := strings.IndexByte(marked, '|')
	require.NotEqual(t, -1, offset)
	text := marked[:offset] + marked[offset+1:]
	return text, positionAtByte(text, offset)
}
