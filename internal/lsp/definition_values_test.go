package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateReferencesVariable(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		path  []string
		found bool
	}{
		{name: "dot", text: `{{ .vars.features.level }}`, path: []string{"features", "level"}, found: true},
		{name: "index", text: `{{ index .vars.annotations "app.kubernetes.io/managed-by" }}`, path: []string{"annotations", "app.kubernetes.io/managed-by"}, found: true},
		{name: "escaped index", text: `{{ index .vars "quote\"key" }}`, path: []string{`quote"key`}, found: true},
		{name: "empty index", text: `{{ index .vars "" }}`, path: []string{""}, found: true},
		{name: "alias", text: `{{ $cfg := .vars.features }}{{ $cfg.level }}`, path: []string{"features", "level"}, found: true},
		{name: "similar name", text: `{{ .vars.featured }}`, path: []string{"features"}},
		{name: "template string", text: `{{ printf ".vars.features" }}`, path: []string{"features"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.found, templateReferencesVariable(tt.text, tt.path))
		})
	}
}

func TestValuesVariableDefinition(t *testing.T) {
	tests := []struct {
		name   string
		marked string
		files  []string
		key    string
	}{
		{name: "default only", marked: `{{ .vars.repl|icas }}`, files: []string{bundle.BundleDefaultsFileName}, key: "replicas"},
		{name: "both layers", marked: `{{ .vars.dom|ain }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "domain"},
		{name: "nested override", marked: `{{ .vars.features.le|vel }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "level"},
		{name: "nested default", marked: `{{ .vars.features.audit_|logs }}`, files: []string{bundle.BundleDefaultsFileName}, key: "audit_logs"},
		{name: "parent segment", marked: `{{ .vars.feat|ures.level }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "features"},
		{name: "quoted key", marked: `{{ index .vars.annotations "app.kubernetes.io/man|aged-by" }}`, files: []string{bundle.BundleDefaultsFileName}, key: `"app.kubernetes.io/managed-by"`},
		{name: "multiple index keys", marked: `{{ index .vars "features" "le|vel" }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "level"},
		{name: "parenthesized index", marked: `{{ (index .vars "features").le|vel }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "level"},
		{name: "nested index", marked: `{{ index (index .vars "features") "le|vel" }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "level"},
		{name: "root reference", marked: `{{ range .vars.features }}{{ $.vars.dom|ain }}{{ end }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "domain"},
		{name: "with scope", marked: `{{ with .vars.features }}{{ .le|vel }}{{ end }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "level"},
		{name: "with else restores dot", marked: `{{ with .vars.features }}{{ .level }}{{ else }}{{ .vars.dom|ain }}{{ end }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "domain"},
		{name: "alias", marked: `{{ $cfg := .vars.features }}{{ $cfg.le|vel }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "level"},
		{name: "index alias", marked: `{{ $cfg := index .vars "features" }}{{ $cfg.le|vel }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "level"},
		{name: "pipeline", marked: `{{ .vars.dom|ain | printf "%s" }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "domain"},
		{name: "UTF16", marked: `😀 {{ .vars.dom|ain }}`, files: []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName}, key: "domain"},
		{name: "ordinary YAML", marked: `value: .vars.dom|ain`},
		{name: "template string", marked: `{{ printf ".vars.dom|ain" }}`},
		{name: "template comment", marked: `{{/* .vars.dom|ain */}}`},
		{name: "namespace", marked: `{{ .va|rs.domain }}`},
		{name: "missing variable", marked: `{{ .vars.|missing }}`},
		{name: "range element is not root", marked: `{{ range .vars.features }}{{ .vars.dom|ain }}{{ end }}`},
		{name: "iterator is not collection", marked: `{{ range $key, $item := .vars.features }}{{ $item.le|vel }}{{ end }}`},
		{name: "with changes dot", marked: `{{ with .vars.features }}{{ .vars.dom|ain }}{{ end }}`},
		{name: "conditionally reassigned alias", marked: `{{ $cfg := .vars.features }}{{ if .vars.enabled }}{{ $cfg = .vars.annotations }}{{ end }}{{ $cfg.le|vel }}`},
		{name: "invalid template", marked: `{{ .vars.dom|ain }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, uri := newValuesCompletionStore(t, tt.marked)
			text, pos := hoverPosition(t, tt.marked)
			doc := store.documents[uri]
			doc.Text = text
			store.documents[uri] = doc

			locations := store.definition(t.Context(), documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})

			require.Len(t, locations, len(tt.files))
			for i, target := range locations {
				filename, ok := fileURIPath(target.URI)
				require.True(t, ok)
				assert.Equal(t, tt.files[i], filepath.Base(filename))
				assert.Equal(t, tt.key, definitionSelection(t, store, target))
			}
		})
	}
}

func TestValuesVariableDefinitionHidesReplacedDefaultSubtree(t *testing.T) {
	marked := `{{ .vars.features.le|vel }}`
	store, uri := newValuesCompletionStore(t, marked)
	for candidate, doc := range store.documents {
		if strings.HasSuffix(candidate, bundle.BundleConfigFileName) {
			doc.Text = `variables = { features = "disabled" }`
			store.documents[candidate] = doc
			break
		}
	}
	text, pos := hoverPosition(t, marked)
	doc := store.documents[uri]
	doc.Text = text
	store.documents[uri] = doc

	locations := store.definition(t.Context(), documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})

	assert.Empty(t, locations)
}

func TestValuesVariableDefinitionArchitecture(t *testing.T) {
	store, dir, bundleURI, _ := newDiagnosticWorkspace(t)
	doc := store.documents[bundleURI]
	doc.Text = strings.Replace(doc.Text, `values/app.yaml`, `values/arch-${sys.arch}.yaml`, 1)
	filename, ok := fileURIPath(bundleURI)
	require.True(t, ok)
	writeDiagnosticFile(t, filename, doc.Text)
	delete(store.documents, bundleURI)
	writeDiagnosticFile(t, filepath.Join(dir, bundle.BundleConfigFileName), "options { architecture = \"test-arch\" }\nvariables = { replicas = 3 }")
	text, pos := hoverPosition(t, `{{ .vars.repl|icas }}`)
	uri := localFileURI(filepath.Join(dir, "values/arch-test-arch.yaml"))
	store.documents[uri] = document{URI: uri, Text: text}

	locations := store.definition(t.Context(), documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})

	require.Len(t, locations, 2, "ancestor discovery must use the adjacent config's architecture")
}

func TestValuesFileDefinition(t *testing.T) {
	tests := []struct {
		name       string
		markedList string
		want       []string
	}{
		{name: "relative", markedList: `["values/app.y|aml"]`, want: []string{"values/app.yaml"}},
		{name: "second item", markedList: `["values/app.yaml", "values/|second.yaml"]`, want: []string{"values/second.yaml"}},
		{name: "architecture", markedList: `["values/arch-${sys.arch}.y|aml"]`, want: []string{"values/arch-test-arch.yaml"}},
		{name: "computed list", markedList: `true ? ["values/app.y|aml", "values/second.yaml"] : []`, want: []string{"values/app.yaml", "values/second.yaml"}},
		{name: "missing", markedList: `["values/mis|sing.yaml"]`},
		{name: "directory", markedList: `["|values"]`},
		{name: "list separator", markedList: `["values/app.yaml"|, "values/second.yaml"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, dir, uri, _ := newDiagnosticWorkspace(t)
			for _, filename := range []string{"values/second.yaml", "values/arch-test-arch.yaml"} {
				writeDiagnosticFile(t, filepath.Join(dir, filename), "value: true")
			}
			configURI := localFileURI(filepath.Join(dir, bundle.BundleConfigFileName))
			store.documents[configURI] = document{URI: configURI, Text: `options { architecture = "test-arch" }`}
			doc := store.documents[uri]
			text, pos := hoverPosition(t, strings.Replace(doc.Text, `["values/app.yaml"]`, tt.markedList, 1))
			doc.Text = text
			store.documents[uri] = doc

			locations := store.definition(t.Context(), documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})

			require.Len(t, locations, len(tt.want))
			for i, filename := range tt.want {
				assert.Equal(t, localFileURI(filepath.Join(dir, filename)), locations[i].URI)
				assert.Equal(t, documentRange{}, locations[i].Range)
			}
		})
	}
}

func TestValuesFileDefinitionOutsideBundleAndUnsaved(t *testing.T) {
	store, dir, uri, _ := newDiagnosticWorkspace(t)
	sharedPath := filepath.Join(t.TempDir(), "shared file.yaml")
	sharedURI := localFileURI(sharedPath)
	store.documents[sharedURI] = document{URI: sharedURI, Text: "value: true"}
	for _, path := range []string{sharedPath, relativeDefinitionPath(t, dir, sharedPath)} {
		doc := store.documents[uri]
		marked := fmt.Sprintf("[%q]", filepath.ToSlash(path)+"|")
		text, pos := hoverPosition(t, strings.Replace(doc.Text, `["values/app.yaml"]`, marked, 1))
		original := doc.Text
		doc.Text = text
		store.documents[uri] = doc
		locations := store.definition(t.Context(), documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})
		require.Equal(t, []location{{URI: sharedURI}}, locations)
		doc.Text = original
		store.documents[uri] = doc
	}
}

func TestValuesVariableDefinitionSharedWorkspace(t *testing.T) {
	root, aURI, bURI, valuesURI := sharedDiagnosticWorkspace(t)
	bPath, ok := fileURIPath(bURI)
	require.True(t, ok)
	writeDiagnosticFile(t, filepath.Join(filepath.Dir(bPath), bundle.BundleConfigFileName), `variables = { message = "from b config" }`)
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{RootURI: localFileURI(root)})
	require.NoError(t, store.indexWorkspace(t.Context()))
	text, pos := hoverPosition(t, `message: {{ .vars.mes|sage }}`)
	store.documents[valuesURI] = document{URI: valuesURI, Text: text}
	params := documentPositionParams{TextDocument: textDocumentIdentifier{URI: valuesURI}, Position: &pos}
	aPath, ok := fileURIPath(aURI)
	require.True(t, ok)
	want := []location{
		{URI: localFileURI(filepath.Join(filepath.Dir(aPath), bundle.BundleDefaultsFileName)), Range: documentRange{Start: position{Character: 14}, End: position{Character: 21}}},
		{URI: localFileURI(filepath.Join(filepath.Dir(bPath), bundle.BundleConfigFileName)), Range: documentRange{Start: position{Character: 14}, End: position{Character: 21}}},
	}
	for range 5 {
		assert.Equal(t, want, store.definition(t.Context(), params), "all unopened owners must contribute, in stable order")
	}

	// A changed bundle buffer removes the reference immediately, without a save.
	aDoc, err := store.sourceDocument(aPath)
	require.NoError(t, err)
	aDoc.Text = strings.Replace(aDoc.Text, `values_files = ["../shared/values.yaml"]`, `values_files = []`, 1)
	store.documents[aURI] = aDoc
	assert.Equal(t, want[1:], store.definition(t.Context(), params))
}

func TestValuesVariableDefinitionDiskAndBuffer(t *testing.T) {
	store, dir, _, valuesURI := newDiagnosticWorkspace(t)
	text, pos := hoverPosition(t, `{{ .vars.repl|icas }}`)
	store.documents[valuesURI] = document{URI: valuesURI, Text: text}
	params := documentPositionParams{TextDocument: textDocumentIdentifier{URI: valuesURI}, Position: &pos}
	locations := store.definition(t.Context(), params)
	require.Len(t, locations, 2)
	configPath := filepath.Join(dir, bundle.BundleConfigFileName)
	configURI := localFileURI(configPath)
	store.documents[configURI] = document{URI: configURI, Text: "# 😀 unsaved\nvariables = { replicas = 5 }"}
	locations = store.definition(t.Context(), params)
	require.Len(t, locations, 2)
	assert.Equal(t, 1, locations[0].Range.Start.Line)
	assert.Equal(t, "replicas", definitionSelection(t, store, locations[0]))
	delete(store.documents, configURI)
	require.NoError(t, os.Remove(configPath))
	assert.Len(t, store.definition(t.Context(), params), 1)
}

func TestComputedVariableDefinition(t *testing.T) {
	store, uri := newValuesCompletionStore(t, "")
	text, pos := hoverPosition(t, `{{ .vars.features.le|vel }}`)
	store.documents[uri] = document{URI: uri, Text: text}
	filename, ok := fileURIPath(uri)
	require.True(t, ok)
	configURI := localFileURI(filepath.Join(filepath.Dir(filepath.Dir(filename)), bundle.BundleConfigFileName))
	store.documents[configURI] = document{URI: configURI, Text: `variables = { features = true ? { level = "debug" } : { level = "info" } }`}
	locations := store.definition(t.Context(), documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})
	require.Len(t, locations, 2)
	assert.Equal(t, `true ? { level = "debug" } : { level = "info" }`, definitionSelection(t, store, locations[0]))

	text, pos = hoverPosition(t, `{{ .vars.features.audit_|logs }}`)
	values := store.documents[uri]
	values.Text = text
	store.documents[uri] = values
	locations = store.definition(t.Context(), documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})
	require.Len(t, locations, 1, "computed objects must preserve unmatched defaults")
	filename, ok = fileURIPath(locations[0].URI)
	require.True(t, ok)
	assert.Equal(t, bundle.BundleDefaultsFileName, filepath.Base(filename))

	text, pos = hoverPosition(t, `{{ .vars.features.le|vel }}`)
	values = store.documents[uri]
	values.Text = text
	store.documents[uri] = values
	config := store.documents[configURI]
	config.Text = `variables = { features = file("features.txt") }`
	store.documents[configURI] = config
	locations = store.definition(t.Context(), documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})
	assert.Empty(t, locations, "file() returns a scalar that replaces the defaults subtree")
}

func TestServeMultipleDefinitions(t *testing.T) {
	store, _, _, uri := newDiagnosticWorkspace(t)
	text, pos := hoverPosition(t, `{{ .vars.repl|icas }}`)
	store.documents[uri] = document{URI: uri, Text: text}
	params, err := json.Marshal(documentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos})
	require.NoError(t, err)
	var output bytes.Buffer
	_, _, err = handleMessage(t.Context(), bufio.NewWriter(&output), message{JSONRPC: jsonRPCVersion, ID: json.RawMessage(`"definition"`), Method: "textDocument/definition", Params: params}, stateInitialized, store, iostreams.IOStreams{})
	require.NoError(t, err)
	responses := decodeResponses(t, &output)
	require.Len(t, responses, 1)
	require.Nil(t, responses[0].Error)
	var locations []location
	require.NoError(t, json.Unmarshal(responses[0].Result, &locations))
	require.Len(t, locations, 2)
	assert.Equal(t, bundle.BundleConfigFileName, filepath.Base(strings.TrimPrefix(locations[0].URI, "file://")))
}

func definitionSelection(t *testing.T, store *documentStore, target location) string {
	t.Helper()
	filename, ok := fileURIPath(target.URI)
	require.True(t, ok)
	doc, err := store.sourceDocument(filename)
	require.NoError(t, err)
	start, ok := byteAtPosition(doc.Text, target.Range.Start)
	require.True(t, ok)
	end, ok := byteAtPosition(doc.Text, target.Range.End)
	require.True(t, ok)
	return doc.Text[start:end]
}

func relativeDefinitionPath(t *testing.T, dir, target string) string {
	t.Helper()
	path, err := filepath.Rel(dir, target)
	require.NoError(t, err)
	return path
}
