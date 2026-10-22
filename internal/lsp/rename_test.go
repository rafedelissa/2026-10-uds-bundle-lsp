// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"testing"

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

			result, err := store.rename(renameParams{
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

func TestRenameDoesNotScanPastWhitespace(t *testing.T) {
	text, pos := hoverPosition(t, `locals { repo = "example.com" }
package "app" { source = local.repo |}
`)
	uri := "file:///workspace/bundle.uds.hcl"
	store := newDocumentStore()
	store.documents[uri] = document{URI: uri, Text: text}
	_, err := store.rename(renameParams{
		TextDocument: textDocumentIdentifier{URI: uri}, Position: &pos, NewName: "registry",
	})
	require.ErrorContains(t, err, "no renameable symbol")
}
