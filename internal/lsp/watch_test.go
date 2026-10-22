package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeFileWatcherRegistration(t *testing.T) {
	for _, reply := range []string{
		`{"jsonrpc":"2.0","id":"uds-lsp-watch-files","result":null}`,
		`{"jsonrpc":"2.0","id":"uds-lsp-watch-files","error":{"code":-32603,"message":"watching unavailable"}}`,
	} {
		t.Run(reply, func(t *testing.T) {
			input := framedMessages(t,
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"workspace":{"didChangeWatchedFiles":{"dynamicRegistration":true}}}}}`,
				`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
				reply,
				`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
				`{"jsonrpc":"2.0","method":"workspace/didChangeWatchedFiles","params":{"changes":[]}}`,
				`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
				`{"jsonrpc":"2.0","method":"exit"}`,
			)
			var output bytes.Buffer
			require.NoError(t, Serve(t.Context(), input, &output, iostreams.IOStreams{}))
			reader := bufio.NewReader(&output)
			var messages []map[string]json.RawMessage
			for {
				payload, err := readMessage(reader)
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)
				var msg map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(payload, &msg))
				messages = append(messages, msg)
			}
			require.Len(t, messages, 3, "register exactly once, and do not reply to client responses")
			assert.JSONEq(t, `"client/registerCapability"`, string(messages[1]["method"]))
			assert.JSONEq(t, `{"registrations":[{"id":"uds-lsp-watch-files","method":"workspace/didChangeWatchedFiles","registerOptions":{"watchers":[{"globPattern":"**/*","kind":7}]}}]}`, string(messages[1]["params"]))
			assert.JSONEq(t, `2`, string(messages[2]["id"]))
		})
	}
}
