// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentHighlight(t *testing.T) {
	tests := []struct {
		name   string
		marked string
		count  int
	}{
		{
			name: "local",
			marked: `locals { repo = "example.com" }
package "app" { source = "${local.re|po}/${local.repo}" }
`,
			count: 3,
		},
		{
			name: "package",
			marked: `package "base" { source = "base" }
package "app" {
  source = "app"
  depends_on = [package.b|ase]
}
`,
			count: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.marked)
			uri := "file:///workspace/bundle.uds.hcl"
			store := newDocumentStore()
			store.documents[uri] = document{URI: uri, Text: text}

			result := store.highlight(documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri},
				Position:     &pos,
			})

			require.Len(t, result, tt.count)
			assert.Equal(t, documentHighlightKindWrite, result[0].Kind)
			for _, highlight := range result[1:] {
				assert.Equal(t, documentHighlightKindRead, highlight.Kind)
			}
		})
	}
}
