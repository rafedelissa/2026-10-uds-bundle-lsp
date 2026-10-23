// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"os"
	"strings"
	"testing"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRename(t *testing.T) {
	tests := []struct {
		name        string
		marked      string
		wantOldText []string
		wantNewText []string
	}{
		{
			name: "local",
			marked: `locals { repo = "example.com" }
package "app" { source = "${local.re|po}/${local.repo}" }
`,
			wantOldText: []string{"repo", "repo", "repo"},
			wantNewText: []string{"registry", "registry", "registry"},
		},
		{
			name: "local reference at end of name",
			marked: `locals { repo = "example.com" }
package "app" { source = "${local.repo|}/${local.repo}" }
`,
			wantOldText: []string{"repo", "repo", "repo"},
			wantNewText: []string{"registry", "registry", "registry"},
		},
		{
			name: "local declaration at end of name",
			marked: `locals { repo| = "example.com" }
package "app" { source = local.repo }
`,
			wantOldText: []string{"repo", "repo"},
			wantNewText: []string{"registry", "registry"},
		},
		{
			name: "unicode reference at end of name",
			marked: `locals { café = "example.com" }
package "😀app" { source = "${local.café|}" }
`,
			wantOldText: []string{"café", "café"},
			wantNewText: []string{"registry", "registry"},
		},
		{
			name: "package",
			marked: `package "base" { source = "base" }
package "app" {
  source = "app"
  depends_on = [package.b|ase]
}
`,
			wantOldText: []string{`"base"`, "base"},
			wantNewText: []string{`"registry"`, "registry"},
		},
		{
			name: "package reference at end of name",
			marked: `package "base" { source = "base" }
package "app" {
  source = "app"
  depends_on = [package.base|]
}
`,
			wantOldText: []string{`"base"`, "base"},
			wantNewText: []string{`"registry"`, "registry"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.marked)
			uri := "file:///workspace/bundle.uds.hcl"
			store := newDocumentStore()
			store.documents[uri] = document{URI: uri, Text: text}

			result, err := store.rename(t.Context(), renameParams{
				TextDocument: textDocumentIdentifier{URI: uri},
				Position:     &pos,
				NewName:      "registry",
			})

			require.NoError(t, err)
			edits := result.Changes[uri]
			oldText := make([]string, 0, len(edits))
			newText := make([]string, 0, len(edits))
			for _, edit := range edits {
				start, ok := byteAtPosition(text, edit.Range.Start)
				require.True(t, ok)
				end, ok := byteAtPosition(text, edit.Range.End)
				require.True(t, ok)
				oldText = append(oldText, text[start:end])
				newText = append(newText, edit.NewText)
			}
			assert.ElementsMatch(t, tt.wantOldText, oldText)
			assert.ElementsMatch(t, tt.wantNewText, newText)
		})
	}
}

func TestRenameVariableAcrossFiles(t *testing.T) {
	values := `direct: {{ .vars.features.level }}
aliased: {{ $cfg := .vars.features }}{{ $cfg.level }}
indexed: {{ index .vars "features" "level" }}
`
	store, valuesURI := newValuesCompletionStore(t, values)
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
	doc := store.documents[configURI]
	marked := strings.Replace(doc.Text, "level =", "le|vel =", 1)
	text, pos := hoverPosition(t, marked)
	doc.Text = text
	store.documents[configURI] = doc

	result, err := store.rename(t.Context(), renameParams{
		TextDocument: textDocumentIdentifier{URI: configURI},
		Position:     &pos,
		NewName:      "verbosity",
	})

	require.NoError(t, err)
	require.Len(t, result.Changes[configURI], 1)
	require.Len(t, result.Changes[defaultsURI], 1)
	require.Len(t, result.Changes[valuesURI], 3)
	for _, uri := range []string{configURI, defaultsURI, valuesURI} {
		source := store.documents[uri].Text
		for _, edit := range result.Changes[uri] {
			start, ok := byteAtPosition(source, edit.Range.Start)
			require.True(t, ok)
			end, ok := byteAtPosition(source, edit.Range.End)
			require.True(t, ok)
			assert.Equal(t, "level", source[start:end])
			assert.Equal(t, "verbosity", edit.NewText)
		}
	}
}

func TestRenameVariableFromValuesFile(t *testing.T) {
	store, valuesURI := newValuesCompletionStore(t, `value: {{ .vars.dom|ain }}`)
	text, pos := hoverPosition(t, store.documents[valuesURI].Text)
	doc := store.documents[valuesURI]
	doc.Text = text
	store.documents[valuesURI] = doc

	result, err := store.rename(t.Context(), renameParams{
		TextDocument: textDocumentIdentifier{URI: valuesURI},
		Position:     &pos,
		NewName:      "hostname",
	})

	require.NoError(t, err)
	assert.Len(t, result.Changes, 3)
	for _, edits := range result.Changes {
		require.NotEmpty(t, edits)
		for _, edit := range edits {
			assert.Equal(t, "hostname", edit.NewText)
		}
	}
}

func TestRenameIndexedVariableAcrossFiles(t *testing.T) {
	store, valuesURI := newValuesCompletionStore(t, `value: {{ index .vars "invalid-|key" }}`)
	text, pos := hoverPosition(t, store.documents[valuesURI].Text)
	doc := store.documents[valuesURI]
	doc.Text = text
	store.documents[valuesURI] = doc

	result, err := store.rename(t.Context(), renameParams{
		TextDocument: textDocumentIdentifier{URI: valuesURI},
		Position:     &pos,
		NewName:      "valid_key",
	})

	require.NoError(t, err)
	assert.Len(t, result.Changes, 2)
	valuesEdits := result.Changes[valuesURI]
	require.Len(t, valuesEdits, 1)
	assert.Equal(t, "valid_key", valuesEdits[0].NewText)
	start, ok := byteAtPosition(text, valuesEdits[0].Range.Start)
	require.True(t, ok)
	end, ok := byteAtPosition(text, valuesEdits[0].Range.End)
	require.True(t, ok)
	assert.Equal(t, "invalid-key", text[start:end])
}

func TestRenameVariableRejectsCollision(t *testing.T) {
	for _, tt := range []struct {
		name    string
		marked  string
		newName string
	}{
		{name: "same layer", marked: `value: {{ .vars.dom|ain }}`, newName: "features"},
		{name: "other layer", marked: `value: {{ .vars.repl|icas }}`, newName: "domain"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, valuesURI := newValuesCompletionStore(t, tt.marked)
			text, pos := hoverPosition(t, store.documents[valuesURI].Text)
			doc := store.documents[valuesURI]
			doc.Text = text
			store.documents[valuesURI] = doc

			result, err := store.rename(t.Context(), renameParams{
				TextDocument: textDocumentIdentifier{URI: valuesURI},
				Position:     &pos,
				NewName:      tt.newName,
			})

			require.ErrorContains(t, err, "already defined")
			assert.Nil(t, result)
		})
	}
}

func TestRenameVariableRejectsConfigAncestorReplacement(t *testing.T) {
	store, valuesURI := newValuesCompletionStore(t, `value: {{ .vars.features.audit_|logs }}`)
	var configURI string
	for uri, doc := range store.documents {
		if strings.HasSuffix(uri, bundle.BundleConfigFileName) {
			configURI = uri
			doc.Text = `variables = { features = "disabled" }`
			store.documents[uri] = doc
			break
		}
	}
	require.NotEmpty(t, configURI)
	text, pos := hoverPosition(t, store.documents[valuesURI].Text)
	doc := store.documents[valuesURI]
	doc.Text = text
	store.documents[valuesURI] = doc

	result, err := store.rename(t.Context(), renameParams{
		TextDocument: textDocumentIdentifier{URI: valuesURI},
		Position:     &pos,
		NewName:      "audit",
	})

	require.ErrorContains(t, err, "replaced by a config ancestor")
	assert.Nil(t, result)
}

func TestRenameVariableRejectsPartiallyComputedDefinition(t *testing.T) {
	store, valuesURI := newValuesCompletionStore(t, `value: {{ .vars.features.le|vel }}`)
	for uri, doc := range store.documents {
		if strings.HasSuffix(uri, bundle.BundleConfigFileName) {
			doc.Text = `variables = { features = true ? { level = "debug" } : { level = "info" } }`
			store.documents[uri] = doc
			break
		}
	}
	text, pos := hoverPosition(t, store.documents[valuesURI].Text)
	doc := store.documents[valuesURI]
	doc.Text = text
	store.documents[valuesURI] = doc

	result, err := store.rename(t.Context(), renameParams{
		TextDocument: textDocumentIdentifier{URI: valuesURI},
		Position:     &pos,
		NewName:      "verbosity",
	})

	require.ErrorContains(t, err, "has no literal key in config.uds.hcl")
	assert.Nil(t, result)
}

func TestRenameVariableRejectsIncompleteDiscovery(t *testing.T) {
	store, valuesURI := newValuesCompletionStore(t, "value: true\n")
	require.NotEmpty(t, valuesURI)
	var bundleURI, configURI string
	for uri := range store.documents {
		switch {
		case strings.HasSuffix(uri, bundle.BundleFileName):
			bundleURI = uri
		case strings.HasSuffix(uri, bundle.BundleConfigFileName):
			configURI = uri
		}
	}
	require.NotEmpty(t, bundleURI)
	require.NotEmpty(t, configURI)
	bundleDoc := store.documents[bundleURI]
	bundleDoc.Text = `package "app" {`
	store.documents[bundleURI] = bundleDoc
	configDoc := store.documents[configURI]
	marked := strings.Replace(configDoc.Text, "domain =", "dom|ain =", 1)
	text, pos := hoverPosition(t, marked)
	configDoc.Text = text
	store.documents[configURI] = configDoc

	result, err := store.rename(t.Context(), renameParams{
		TextDocument: textDocumentIdentifier{URI: configURI},
		Position:     &pos,
		NewName:      "hostname",
	})

	require.ErrorContains(t, err, "parse bundle")
	assert.Nil(t, result)
}

func TestRenameVariableRejectsUnreadableLayer(t *testing.T) {
	store, valuesURI := newValuesCompletionStore(t, "value: true\n")
	require.NotEmpty(t, valuesURI)
	var configURI, defaultsURI string
	for uri := range store.documents {
		switch {
		case strings.HasSuffix(uri, bundle.BundleConfigFileName):
			configURI = uri
		case strings.HasSuffix(uri, bundle.BundleDefaultsFileName):
			defaultsURI = uri
		}
	}
	configPath, ok := fileURIPath(configURI)
	require.True(t, ok)
	delete(store.documents, configURI)
	require.NoError(t, os.Mkdir(configPath, 0o700))
	defaultsDoc := store.documents[defaultsURI]
	marked := strings.Replace(defaultsDoc.Text, "replicas =", "repl|icas =", 1)
	text, pos := hoverPosition(t, marked)
	defaultsDoc.Text = text
	store.documents[defaultsURI] = defaultsDoc

	result, err := store.rename(t.Context(), renameParams{
		TextDocument: textDocumentIdentifier{URI: defaultsURI},
		Position:     &pos,
		NewName:      "count",
	})

	require.ErrorContains(t, err, "read config.uds.hcl for rename")
	assert.Nil(t, result)
}

func TestRenameDoesNotScanPastWhitespace(t *testing.T) {
	text, pos := hoverPosition(t, `locals { repo = "example.com" }
package "app" { source = local.repo |}
`)
	uri := "file:///workspace/bundle.uds.hcl"
	store := newDocumentStore()
	store.documents[uri] = document{URI: uri, Text: text}
	result, err := store.rename(t.Context(), renameParams{
		TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos, NewName: "registry",
	})
	require.ErrorContains(t, err, "no renameable symbol")
	assert.Nil(t, result)
}
