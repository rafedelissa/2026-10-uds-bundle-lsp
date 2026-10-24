// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReferencesInBundle(t *testing.T) {
	text, pos := hoverPosition(t, `locals { repo = "example.com" }
package "app" { source = "${local.re|po}/${local.repo}" }
`)
	uri := "file:///workspace/bundle.uds.hcl"
	store := newDocumentStore()
	store.documents[uri] = document{URI: uri, Text: text}

	withoutDeclaration, err := store.references(t.Context(), referencesParams{
		TextDocument: textDocumentIdentifier{URI: uri},
		Position:     &pos,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"repo", "repo"}, locationSelections(t, store, withoutDeclaration))

	withDeclaration, err := store.references(t.Context(), referencesParams{
		TextDocument: textDocumentIdentifier{URI: uri},
		Position:     &pos,
		Context:      referenceContext{IncludeDeclaration: true},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"repo", "repo", "repo"}, locationSelections(t, store, withDeclaration))
}

func TestReferencesFromBundleDeclaration(t *testing.T) {
	text, pos := hoverPosition(t, `package "base|" { source = "base" }
package "app" {
  source = "app"
  depends_on = [package.base]
}
`)
	uri := "file:///workspace/bundle.uds.hcl"
	store := newDocumentStore()
	store.documents[uri] = document{URI: uri, Text: text}

	locations, err := store.references(t.Context(), referencesParams{
		TextDocument: textDocumentIdentifier{URI: uri},
		Position:     &pos,
		Context:      referenceContext{IncludeDeclaration: true},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{`"base"`, "base"}, locationSelections(t, store, locations))
}

func TestValuesVariableReferencesAcrossFiles(t *testing.T) {
	values := `direct: {{ .vars.features.level }}
aliased: {{ $cfg := .vars.features }}{{ $cfg.level }}
indexed: {{ index .vars "features" "level" }}
`
	store, valuesURI := newValuesCompletionStore(t, values)
	configURI := documentURIWithSuffix(t, store, bundle.BundleConfigFileName)
	config := store.documents[configURI]
	marked := strings.Replace(config.Text, "level =", "le|vel =", 1)
	text, pos := hoverPosition(t, marked)
	config.Text = text
	store.documents[configURI] = config

	locations, err := store.references(t.Context(), referencesParams{
		TextDocument: textDocumentIdentifier{URI: configURI},
		Position:     &pos,
		Context:      referenceContext{IncludeDeclaration: true},
	})

	require.NoError(t, err)
	require.Len(t, locations, 5)
	assert.Equal(t, []string{"level", "level", "level", "level", "level"}, locationSelections(t, store, locations))
	assert.Equal(t, valuesURI, locations[2].URI)
}

func TestValuesVariableReferencesIgnoreUnrelatedInvalidBundle(t *testing.T) {
	store, valuesURI := newValuesCompletionStore(t, `value: {{ .vars.dom|ain }}`)
	text, pos := hoverPosition(t, store.documents[valuesURI].Text)
	values := store.documents[valuesURI]
	values.Text = text
	store.documents[valuesURI] = values
	invalidURI := localFileURI(filepath.Join(t.TempDir(), bundle.BundleFileName))
	store.documents[invalidURI] = document{URI: invalidURI, Text: `package "broken" {`}
	invalidConfigDir := t.TempDir()
	invalidConfigBundleURI := localFileURI(filepath.Join(invalidConfigDir, bundle.BundleFileName))
	store.documents[invalidConfigBundleURI] = document{URI: invalidConfigBundleURI, Text: `
uds { bundle_api_version = "uds.dev/v1alpha1" }
metadata { name = "invalid-config" }
package "app" { source = "app" }
`}
	invalidConfigURI := localFileURI(filepath.Join(invalidConfigDir, bundle.BundleConfigFileName))
	store.documents[invalidConfigURI] = document{URI: invalidConfigURI, Text: `variables = { nested = null }`}

	locations, err := store.references(t.Context(), referencesParams{
		TextDocument: textDocumentIdentifier{URI: valuesURI},
		Position:     &pos,
		Context:      referenceContext{IncludeDeclaration: true},
	})

	require.NoError(t, err)
	require.Len(t, locations, 3)
	assert.Equal(t, []string{"domain", "domain", "domain"}, locationSelections(t, store, locations))
}

func TestValuesVariableReferencesRespectConfigAncestorReplacement(t *testing.T) {
	store, valuesURI := newValuesCompletionStore(t, `value: {{ .vars.features.le|vel }}`)
	configURI := documentURIWithSuffix(t, store, bundle.BundleConfigFileName)
	config := store.documents[configURI]
	config.Text = `variables = { features = "disabled" }`
	store.documents[configURI] = config
	defaultsURI := documentURIWithSuffix(t, store, bundle.BundleDefaultsFileName)
	text, pos := hoverPosition(t, store.documents[valuesURI].Text)
	values := store.documents[valuesURI]
	values.Text = text
	store.documents[valuesURI] = values

	locations, err := store.references(t.Context(), referencesParams{
		TextDocument: textDocumentIdentifier{URI: valuesURI},
		Position:     &pos,
		Context:      referenceContext{IncludeDeclaration: true},
	})
	require.NoError(t, err)
	assert.Empty(t, locations, "a replaced defaults subtree must not supply a declaration or usages")

	defaults := store.documents[defaultsURI]
	marked := strings.Replace(defaults.Text, "level =", "le|vel =", 1)
	defaults.Text, pos = hoverPosition(t, marked)
	store.documents[defaultsURI] = defaults
	locations, err = store.references(t.Context(), referencesParams{
		TextDocument: textDocumentIdentifier{URI: defaultsURI},
		Position:     &pos,
		Context:      referenceContext{IncludeDeclaration: true},
	})
	require.NoError(t, err)
	require.Len(t, locations, 1)
	assert.Equal(t, defaultsURI, locations[0].URI)
	assert.Equal(t, "level", locationSelections(t, store, locations)[0])
}

func TestValuesVariableReferencesSharedFile(t *testing.T) {
	root, _, bURI, valuesURI := sharedDiagnosticWorkspace(t)
	bPath, ok := fileURIPath(bURI)
	require.True(t, ok)
	writeDiagnosticFile(t, filepath.Join(filepath.Dir(bPath), bundle.BundleConfigFileName), `variables = { message = "from b config" }`)
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{RootURI: localFileURI(root)})
	require.NoError(t, store.indexWorkspace(t.Context()))
	text, pos := hoverPosition(t, `message: {{ .vars.mes|sage }}`)
	store.documents[valuesURI] = document{URI: valuesURI, Text: text}

	locations, err := store.references(t.Context(), referencesParams{
		TextDocument: textDocumentIdentifier{URI: valuesURI},
		Position:     &pos,
		Context:      referenceContext{IncludeDeclaration: true},
	})

	require.NoError(t, err)
	require.Len(t, locations, 3)
	assert.Equal(t, []string{"message", "message", "message"}, locationSelections(t, store, locations))
	assert.Equal(t, valuesURI, locations[2].URI, "the shared usage must only be returned once")
}

func TestValuesVariableReferencesExcludeDeclarations(t *testing.T) {
	store, valuesURI := newValuesCompletionStore(t, `value: {{ .vars.dom|ain }}`)
	text, pos := hoverPosition(t, store.documents[valuesURI].Text)
	doc := store.documents[valuesURI]
	doc.Text = text
	store.documents[valuesURI] = doc

	locations, err := store.references(t.Context(), referencesParams{
		TextDocument: textDocumentIdentifier{URI: valuesURI},
		Position:     &pos,
	})

	require.NoError(t, err)
	require.Len(t, locations, 1)
	assert.Equal(t, valuesURI, locations[0].URI)
	assert.Equal(t, "domain", locationSelections(t, store, locations)[0])
}

func documentURIWithSuffix(t *testing.T, store *documentStore, suffix string) string {
	t.Helper()
	for uri := range store.documents {
		if strings.HasSuffix(uri, suffix) {
			return uri
		}
	}
	t.Fatalf("no document URI ends with %q", suffix)
	return ""
}

func locationSelections(t *testing.T, store *documentStore, locations []location) []string {
	t.Helper()
	result := make([]string, 0, len(locations))
	for _, target := range locations {
		result = append(result, definitionSelection(t, store, target))
	}
	return result
}
