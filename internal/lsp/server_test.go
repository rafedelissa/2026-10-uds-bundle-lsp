// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/defenseunicorns/uds-cli/internal/logger"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeLifecycle(t *testing.T) {
	input := framedMessages(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer

	err := Serve(context.Background(), input, &output, iostreams.IOStreams{})
	require.NoError(t, err)

	responses := decodeResponses(t, &output)
	require.Len(t, responses, 2)
	assert.JSONEq(t, `1`, string(responses[0].ID))
	assert.Nil(t, responses[0].Error)
	assert.JSONEq(t, `{
		"capabilities": {
			"completionProvider": {"resolveProvider": false, "triggerCharacters": ["{", ".", "\""]},
			"definitionProvider": true,
			"documentHighlightProvider": true,
			"hoverProvider": true,
			"positionEncoding": "utf-16",
			"renameProvider": true,
			"textDocumentSync": {"openClose": true, "change": 1, "save": {"includeText": false}}
		},
		"serverInfo": {"name": "uds-lsp"}
	}`, string(responses[0].Result))
	assert.JSONEq(t, `2`, string(responses[1].ID))
	assert.JSONEq(t, `null`, string(responses[1].Result))
}

func TestServeRequestErrors(t *testing.T) {
	input := framedMessages(t,
		`{"jsonrpc":"2.0","id":"early","method":"textDocument/hover"}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":"unknown","method":"unknown/method"}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer

	err := Serve(context.Background(), input, &output, iostreams.IOStreams{})
	require.NoError(t, err)

	responses := decodeResponses(t, &output)
	require.Len(t, responses, 4)
	require.NotNil(t, responses[0].Error)
	assert.Equal(t, codeServerNotInitialized, responses[0].Error.Code)
	require.NotNil(t, responses[2].Error)
	assert.Equal(t, codeMethodNotFound, responses[2].Error.Code)
}

func TestServeCorrelatesInvalidRequestsAndIgnoresNotifications(t *testing.T) {
	input := framedMessages(t,
		`{"jsonrpc":"2.0","method":"initialize"}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":"duplicate","method":"initialize"}`,
		`{"jsonrpc":"2.0","id":"initialized-request","method":"initialized"}`,
		`{"jsonrpc":"2.0","method":"shutdown"}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","id":"exit-request","method":"exit"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer

	err := Serve(context.Background(), input, &output, iostreams.IOStreams{})
	require.NoError(t, err)

	responses := decodeResponses(t, &output)
	require.Len(t, responses, 5)
	assert.JSONEq(t, `1`, string(responses[0].ID))
	assert.JSONEq(t, `"duplicate"`, string(responses[1].ID))
	require.NotNil(t, responses[1].Error)
	assert.Equal(t, codeInvalidRequest, responses[1].Error.Code)
	assert.JSONEq(t, `"initialized-request"`, string(responses[2].ID))
	require.NotNil(t, responses[2].Error)
	assert.Equal(t, codeInvalidRequest, responses[2].Error.Code)
	assert.JSONEq(t, `2`, string(responses[3].ID))
	assert.JSONEq(t, `"exit-request"`, string(responses[4].ID))
	require.NotNil(t, responses[4].Error)
	assert.Equal(t, codeInvalidRequest, responses[4].Error.Code)
}

func TestServeCancellationInterruptsRead(t *testing.T) {
	input, writer := io.Pipe()
	t.Cleanup(func() {
		require.NoError(t, writer.Close())
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Serve(ctx, input, io.Discard, iostreams.IOStreams{})
	}()

	cancel()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop after context cancellation")
	}
}

func TestServeExitBeforeShutdown(t *testing.T) {
	input := framedMessages(t, `{"jsonrpc":"2.0","method":"exit"}`)

	err := Serve(context.Background(), input, io.Discard, iostreams.IOStreams{})

	require.ErrorIs(t, err, ErrExitBeforeShutdown)
}

func TestServeParseError(t *testing.T) {
	input := framedMessages(t,
		`not-json`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	)
	var output bytes.Buffer

	err := Serve(context.Background(), input, &output, iostreams.IOStreams{})
	require.NoError(t, err)

	responses := decodeResponses(t, &output)
	require.Len(t, responses, 3)
	require.NotNil(t, responses[0].Error)
	assert.Equal(t, codeParseError, responses[0].Error.Code)
	assert.JSONEq(t, `null`, string(responses[0].ID))
}

func TestServeLogsClientInfo(t *testing.T) {
	tests := []struct {
		name    string
		params  string
		client  string
		version string
	}{
		{name: "client and version", params: `{"clientInfo":{"name":"Visual Studio Code","version":"1.105.0"}}`, client: "Visual Studio Code", version: "1.105.0"},
		{name: "optional version", params: `{"clientInfo":{"name":"test-client"}}`, client: "test-client", version: "unknown"},
		{name: "optional client info", params: `{}`, client: "unknown", version: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streams, _, _, logs := iostreams.NewTestIOStreams()
			streams = logger.Bind(streams, "info")
			input := framedMessages(t,
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":`+tt.params+`}`,
				`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
				`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
				`{"jsonrpc":"2.0","method":"exit"}`,
			)
			var output bytes.Buffer
			require.NoError(t, Serve(t.Context(), input, &output, streams))
			assert.Contains(t, logs.String(), "LSP initialize received")
			assert.Contains(t, logs.String(), "client_name")
			assert.Contains(t, logs.String(), tt.client)
			assert.Contains(t, logs.String(), "client_version")
			assert.Contains(t, logs.String(), tt.version)
			// Parsing the output verifies logs never corrupt the protocol stream.
			assert.Len(t, decodeResponses(t, &output), 2)
		})
	}
}

func framedMessages(t *testing.T, messages ...string) io.ReadCloser {
	t.Helper()

	var input bytes.Buffer
	for _, message := range messages {
		_, err := fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(message), message)
		require.NoError(t, err)
	}
	return io.NopCloser(&input)
}

func decodeResponses(t *testing.T, output *bytes.Buffer) []response {
	t.Helper()

	reader := bufio.NewReaderSize(output, maxHeaderLineSize+1)
	responses := make([]response, 0)
	for {
		payload, err := readMessage(reader)
		if errors.Is(err, io.EOF) {
			return responses
		}
		require.NoError(t, err)

		var decoded response
		require.NoError(t, json.Unmarshal(payload, &decoded))
		responses = append(responses, decoded)
	}
}
