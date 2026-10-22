// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"strings"
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
			lines := strings.Split(text, "\n")
			for _, edit := range edits {
				oldText = append(oldText, lines[edit.Range.Start.Line][edit.Range.Start.Character:edit.Range.End.Character])
				newText = append(newText, edit.NewText)
			}
			assert.ElementsMatch(t, tt.wantOldText, oldText)
			assert.ElementsMatch(t, tt.wantNewText, newText)
		})
	}
}
