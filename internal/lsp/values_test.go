package lsp

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValuesFileCompletion(t *testing.T) {
	tests := []struct {
		name     string
		marked   string
		want     []string
		newText  string
		wantText string
	}{
		{name: "namespace", marked: "replicas: {{ .var| }}\n", want: []string{"vars"}, newText: "vars."},
		{name: "top level", marked: "replicas: {{ .vars.| }}\n", want: []string{"annotations", "domain", "features", "replicas", "区域"}},
		{name: "nested", marked: "audit: {{ .vars.features.| }}\n", want: []string{"audit_logs", "level"}},
		{name: "trimmed range", marked: "{{- range .vars.| }}\n{{- end }}\n", want: []string{"annotations", "domain", "features", "replicas", "区域"}},
		{name: "index key", marked: `managedBy: {{ index .vars.annotations "|" }}` + "\n", want: []string{"app.kubernetes.io/managed-by", "team"}, newText: "app.kubernetes.io/managed-by"},
		{
			name:     "index key without closing quote",
			marked:   `managedBy: {{ index .vars.annotations "| }}` + "\n",
			want:     []string{"app.kubernetes.io/managed-by", "team"},
			newText:  `app.kubernetes.io/managed-by"`,
			wantText: `managedBy: {{ index .vars.annotations "app.kubernetes.io/managed-by"}}` + "\n",
		},
		{name: "ordinary YAML", marked: "replicas: 2|\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, uri := newValuesCompletionStore(t, tt.marked)
			text, pos := hoverPosition(t, store.documents[uri].Text)
			doc := store.documents[uri]
			doc.Text = text
			store.documents[uri] = doc

			result := store.complete(t.Context(), documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri},
				Position:     &pos,
			})

			labels := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				labels = append(labels, item.Label)
			}
			if len(tt.want) == 0 {
				assert.Empty(t, labels)
				return
			}
			assert.Equal(t, tt.want, labels)
			if tt.newText != "" {
				item := completionByLabel(t, result, tt.want[0])
				assert.Equal(t, tt.newText, item.TextEdit.NewText)
				if tt.wantText != "" {
					start, ok := byteAtPosition(text, item.TextEdit.Range.Start)
					require.True(t, ok)
					end, ok := byteAtPosition(text, item.TextEdit.Range.End)
					require.True(t, ok)
					assert.Equal(t, tt.wantText, text[:start]+item.TextEdit.NewText+text[end:])
				}
			}
		})
	}
}

func TestValuesFileVariables(t *testing.T) {
	store, uri := newValuesCompletionStore(t, "value: {{ .vars.value }}\n")

	variables := store.variablesForValuesFile(t.Context(), uri)

	require.NotNil(t, variables)
	assert.Contains(t, variables, "replicas")
	assert.Contains(t, variables, "domain")
	features, ok := variables["features"].(bundle.Variables)
	require.True(t, ok)
	assert.Contains(t, features, "audit_logs")
	assert.Contains(t, features, "level")
}

func TestValuesFileCompletionLoadsWorkspaceFiles(t *testing.T) {
	dir, _ := diskValuesWorkspace(t)
	text, pos := hoverPosition(t, "replicas: {{ .vars.| }}\n")
	uri := valuesFileURI(filepath.Join(dir, "values", "app.yaml"))
	store := newDocumentStore()
	store.documents[uri] = document{URI: uri, LanguageID: "yaml", Text: text}

	result := store.complete(t.Context(), documentPositionParams{
		TextDocument: textDocumentIdentifier{URI: uri},
		Position:     &pos,
	})

	assert.Equal(t, "replicas", completionByLabel(t, result, "replicas").TextEdit.NewText)
}

func TestValuesFileCompletionPrefersOpenBundle(t *testing.T) {
	dir, bundlePath := diskValuesWorkspace(t)
	text, pos := hoverPosition(t, "replicas: {{ .vars.| }}\n")
	uri := valuesFileURI(filepath.Join(dir, "values", "app.yaml"))
	store := newDocumentStore()
	bundleURI := valuesFileURI(bundlePath)
	store.documents[bundleURI] = document{URI: bundleURI, LanguageID: "hcl", Text: "package {"}
	store.documents[uri] = document{URI: uri, LanguageID: "yaml", Text: text}

	result := store.complete(t.Context(), documentPositionParams{
		TextDocument: textDocumentIdentifier{URI: uri},
		Position:     &pos,
	})

	assert.Empty(t, result.Items)
}

func TestValuesFileCompletionRequiresBundleReference(t *testing.T) {
	store, _ := newValuesCompletionStore(t, "value: {{ .vars.| }}\n")
	path := filepath.Join(t.TempDir(), "unreferenced.yaml")
	uri := valuesFileURI(path)
	text, pos := hoverPosition(t, "value: {{ .vars.| }}\n")
	store.documents[uri] = document{URI: uri, LanguageID: "yaml", Text: text}

	result := store.complete(t.Context(), documentPositionParams{
		TextDocument: textDocumentIdentifier{URI: uri},
		Position:     &pos,
	})

	assert.Empty(t, result.Items)
}

func diskValuesWorkspace(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "values"), 0o700))
	bundlePath := filepath.Join(dir, bundle.BundleFileName)
	require.NoError(t, os.WriteFile(bundlePath, []byte(`
uds { bundle_api_version = "uds.dev/v1alpha1" }
metadata { name = "example" }
package "app" {
  source = "app"
  values_files = ["values/app.yaml"]
}
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, bundle.BundleDefaultsFileName), []byte(`
variables = { replicas = 2 }
`), 0o600))
	return dir, bundlePath
}

func newValuesCompletionStore(t *testing.T, values string) (*documentStore, string) {
	t.Helper()
	dir := t.TempDir()
	bundleURI := valuesFileURI(filepath.Join(dir, bundle.BundleFileName))
	defaultsURI := valuesFileURI(filepath.Join(dir, bundle.BundleDefaultsFileName))
	configURI := valuesFileURI(filepath.Join(dir, bundle.BundleConfigFileName))
	valuesURI := valuesFileURI(filepath.Join(dir, "values", "app.yaml"))

	store := newDocumentStore()
	store.documents[bundleURI] = document{URI: bundleURI, LanguageID: "hcl", Text: `
uds { bundle_api_version = "uds.dev/v1alpha1" }
metadata {
  name = "example"
  version = "1.0.0"
}
package "app" {
  source = "app"
  values_files = ["values/app.yaml"]
}
`}
	store.documents[defaultsURI] = document{URI: defaultsURI, LanguageID: "hcl", Text: `
variables = {
  replicas = 2
  domain = "uds.dev"
  区域 = "west"
  "invalid-key" = true
  annotations = {
    "app.kubernetes.io/managed-by" = "uds"
    team = "platform"
  }
  features = {
    audit_logs = true
    level = "info"
  }
}
`}
	store.documents[configURI] = document{URI: configURI, LanguageID: "hcl", Text: `
variables = {
  domain = "example.com"
  features = { level = "debug" }
}
`}
	store.documents[valuesURI] = document{URI: valuesURI, LanguageID: "yaml", Text: values}
	return store, valuesURI
}

func valuesFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}
