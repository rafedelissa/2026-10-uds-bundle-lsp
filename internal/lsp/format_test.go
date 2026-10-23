// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatDocument(t *testing.T) {
	const source = "# 🚀\nmetadata {\nname=\"demo\"\nversion=\"1.0.0\"\n}\n"
	const expected = `# 🚀
metadata {
  name    = "demo"
  version = "1.0.0"
}
`
	for _, filename := range []string{bundle.BundleFileName, bundle.BundleConfigFileName, bundle.BundleDefaultsFileName} {
		t.Run(filename, func(t *testing.T) {
			uri := "file:///workspace/" + filename
			store := newDocumentStore()
			store.documents[uri] = document{URI: uri, Text: source}

			edits, err := store.formatDocument(formattingParams{TextDocument: textDocumentIdentifier{URI: uri}})

			require.NoError(t, err)
			require.Len(t, edits, 1)
			assert.Equal(t, documentRange{Start: position{}, End: positionAtByte(source, len(source))}, edits[0].Range)
			assert.Equal(t, expected, edits[0].NewText)
		})
	}
}

func TestFormatDocumentReturnsNoEditsWhenUnchangedOrUnsupported(t *testing.T) {
	const formatted = "metadata {\n  name = \"demo\"\n}\n"
	tests := []struct {
		name string
		uri  string
		text string
	}{
		{name: "already formatted", uri: "file:///workspace/bundle.uds.hcl", text: formatted},
		{name: "referenced values file", uri: "file:///workspace/values.yaml", text: "value: {{ .vars.value }}\n"},
		{name: "unrelated HCL", uri: "file:///workspace/main.tf", text: "value={key=\"value\"}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newDocumentStore()
			store.documents[tt.uri] = document{URI: tt.uri, Text: tt.text}

			edits, err := store.formatDocument(formattingParams{TextDocument: textDocumentIdentifier{URI: tt.uri}})

			require.NoError(t, err)
			assert.Empty(t, edits)
		})
	}
}

func TestHandleFormattingRequest(t *testing.T) {
	const uri = "file:///workspace/bundle.uds.hcl"
	store := newDocumentStore()
	store.documents[uri] = document{URI: uri, Text: "metadata {\nname=\"demo\"\n}\n"}
	params, err := json.Marshal(formattingParams{
		TextDocument: textDocumentIdentifier{URI: uri},
		Options:      formattingOptions{TabSize: 2, InsertSpaces: true},
	})
	require.NoError(t, err)
	var output bytes.Buffer
	writer := bufio.NewWriter(&output)

	stop, state, err := handleMessage(t.Context(), writer, message{
		JSONRPC: jsonRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  "textDocument/formatting",
		Params:  params,
	}, stateInitialized, store, iostreams.IOStreams{})
	require.NoError(t, err)
	require.NoError(t, writer.Flush())

	assert.False(t, stop)
	assert.Equal(t, stateInitialized, state)
	responses := decodeResponses(t, &output)
	require.Len(t, responses, 1)
	assert.Nil(t, responses[0].Error)
	var edits []textEdit
	require.NoError(t, json.Unmarshal(responses[0].Result, &edits))
	require.Len(t, edits, 1)
	assert.Equal(t, "metadata {\n  name = \"demo\"\n}\n", edits[0].NewText)
}

func TestFormatDocumentRequiresOpenDocument(t *testing.T) {
	store := newDocumentStore()

	edits, err := store.formatDocument(formattingParams{TextDocument: textDocumentIdentifier{URI: "file:///workspace/bundle.uds.hcl"}})

	require.ErrorContains(t, err, "is not open")
	assert.Nil(t, edits)
}
