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

	err := Serve(context.Background(), input, &output)
	require.NoError(t, err)

	responses := decodeResponses(t, &output)
	require.Len(t, responses, 2)
	assert.JSONEq(t, `1`, string(responses[0].ID))
	assert.Nil(t, responses[0].Error)
	assert.JSONEq(t, `{"capabilities":{},"serverInfo":{"name":"uds-lsp"}}`, string(responses[0].Result))
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

	err := Serve(context.Background(), input, &output)
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

	err := Serve(context.Background(), input, &output)
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
		result <- Serve(ctx, input, io.Discard)
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

	err := Serve(context.Background(), input, io.Discard)

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

	err := Serve(context.Background(), input, &output)
	require.NoError(t, err)

	responses := decodeResponses(t, &output)
	require.Len(t, responses, 3)
	require.NotNil(t, responses[0].Error)
	assert.Equal(t, codeParseError, responses[0].Error.Code)
	assert.JSONEq(t, `null`, string(responses[0].ID))
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
