// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefinition(t *testing.T) {
	tests := []struct {
		name   string
		marked string
		want   string
	}{
		{
			name: "local",
			marked: `locals { repo = "example.com" }
package "app" { source = local.re|po }
`,
			want: "repo",
		},
		{
			name: "package",
			marked: `package "base" { source = "base" }
package "app" {
  source = "app"
  depends_on = [package.b|ase]
}
`,
			want: "base",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, pos := hoverPosition(t, tt.marked)
			uri := "file:///workspace/bundle.uds.hcl"
			store := newDocumentStore()
			store.documents[uri] = document{URI: uri, Text: text}

			result := store.definition(t.Context(), documentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri},
				Position:     &pos,
			})

			require.Len(t, result, 1)
			assert.Equal(t, uri, result[0].URI)
			start, ok := byteAtPosition(text, result[0].Range.Start)
			require.True(t, ok)
			end, ok := byteAtPosition(text, result[0].Range.End)
			require.True(t, ok)
			assert.Contains(t, text[start:end], tt.want)
		})
	}
}
