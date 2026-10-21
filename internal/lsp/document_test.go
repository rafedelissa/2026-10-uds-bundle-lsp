// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentSynchronizationLifecycle(t *testing.T) {
	store := newDocumentStore()
	var output bytes.Buffer
	writer := bufio.NewWriter(&output)
	uri := "file:///workspace/bundle.uds.hcl"

	handleNotification(t, writer, store, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri": uri, "languageId": "hcl", "version": 1, "text": "initial",
		},
	})
	doc, found := store.document(uri)
	require.True(t, found)
	assert.Equal(t, document{URI: uri, LanguageID: "hcl", Version: 1, Text: "initial"}, doc)

	handleNotification(t, writer, store, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{"text": "changed"}},
	})
	doc, found = store.document(uri)
	require.True(t, found)
	assert.Equal(t, int32(2), doc.Version)
	assert.Equal(t, "changed", doc.Text)

	handleNotification(t, writer, store, "textDocument/didSave", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"text":         "saved",
	})
	doc, found = store.document(uri)
	require.True(t, found)
	assert.Equal(t, "saved", doc.Text)

	handleNotification(t, writer, store, "textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	_, found = store.document(uri)
	assert.False(t, found)

	require.NoError(t, writer.Flush())
	assert.Empty(t, output.Bytes())
}

func TestDocumentSynchronizationRejectsIncrementalChange(t *testing.T) {
	store := newDocumentStore()
	writer := bufio.NewWriter(&bytes.Buffer{})
	uri := "file:///workspace/bundle.uds.hcl"
	handleNotification(t, writer, store, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri": uri, "languageId": "hcl", "version": 1, "text": "initial",
		},
	})
	params, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{
			"range": map[string]any{}, "text": "incremental",
		}},
	})
	require.NoError(t, err)

	_, _, err = handleMessage(writer, message{
		JSONRPC: jsonRPCVersion,
		Method:  "textDocument/didChange",
		Params:  params,
	}, stateInitialized, store)

	require.ErrorContains(t, err, "range is not supported")
}

func handleNotification(
	t *testing.T,
	writer *bufio.Writer,
	store *documentStore,
	method string,
	params any,
) {
	t.Helper()

	encoded, err := json.Marshal(params)
	require.NoError(t, err)
	_, nextState, err := handleMessage(writer, message{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  encoded,
	}, stateInitialized, store)
	require.NoError(t, err)
	assert.Equal(t, stateInitialized, nextState)
}
