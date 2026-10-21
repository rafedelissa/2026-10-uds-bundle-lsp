// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type serverState int

const (
	stateUninitialized serverState = 0
	stateInitialized   serverState = 1
	stateShutdown      serverState = 2
)

var null = json.RawMessage("null")

// InterruptibleReader is an input stream whose Close method unblocks any active
// Read call.
type InterruptibleReader interface {
	io.Reader
	io.Closer
}

// Serve runs an LSP server over the provided input and output streams until the
// client sends an exit notification, closes the input stream, or cancels ctx.
// Serve closes input when ctx is canceled; input must honor the
// InterruptibleReader contract so that cancellation interrupts an active read.
func Serve(ctx context.Context, input InterruptibleReader, output io.Writer) error {
	reader := bufio.NewReaderSize(input, maxHeaderLineSize+1)
	writer := bufio.NewWriter(output)
	state := stateUninitialized

	closeResult := make(chan error, 1)
	stopClose := context.AfterFunc(ctx, func() {
		closeResult <- input.Close()
	})
	defer stopClose()

	for {
		payload, err := readMessage(reader)
		if ctxErr := ctx.Err(); ctxErr != nil {
			if closeErr := <-closeResult; closeErr != nil {
				return errors.Join(ctxErr, fmt.Errorf("close LSP input: %w", closeErr))
			}
			return ctxErr
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		if !json.Valid(payload) {
			if err := writeError(writer, null, codeParseError, "parse error"); err != nil {
				return err
			}
			continue
		}

		var msg message
		if err := json.Unmarshal(payload, &msg); err != nil || msg.JSONRPC != jsonRPCVersion || msg.Method == "" {
			if err := writeError(writer, null, codeInvalidRequest, "invalid request"); err != nil {
				return err
			}
			continue
		}

		shouldExit, nextState, err := handleMessage(writer, msg, state)
		if err != nil {
			return err
		}
		if shouldExit {
			if state != stateShutdown {
				return ErrExitBeforeShutdown
			}
			return nil
		}
		state = nextState
	}
}

func handleMessage(writer *bufio.Writer, msg message, state serverState) (bool, serverState, error) {
	if msg.Method == "exit" {
		if msg.ID != nil {
			return false, state, writeError(writer, msg.ID, codeInvalidRequest, "exit must be a notification")
		}
		return true, state, nil
	}

	if state == stateShutdown {
		if msg.ID == nil {
			return false, state, nil
		}
		return false, state, writeError(writer, msg.ID, codeInvalidRequest, "server has shut down")
	}

	switch msg.Method {
	case "initialize":
		if msg.ID == nil {
			return false, state, nil
		}
		if state != stateUninitialized {
			return false, state, writeError(writer, msg.ID, codeInvalidRequest, "server already initialized")
		}
		result, err := json.Marshal(initializeResult{
			Capabilities: serverCapabilities{},
			ServerInfo:   serverInfo{Name: "uds-lsp"},
		})
		if err != nil {
			return false, state, fmt.Errorf("encode initialize result: %w", err)
		}
		return false, stateInitialized, writeResult(writer, msg.ID, result)
	case "initialized":
		if msg.ID != nil {
			return false, state, writeError(writer, msg.ID, codeInvalidRequest, "initialized must be a notification")
		}
		return false, state, nil
	case "shutdown":
		if msg.ID == nil {
			return false, state, nil
		}
		if state != stateInitialized {
			return false, state, writeError(writer, msg.ID, codeServerNotInitialized, "server not initialized")
		}
		return false, stateShutdown, writeResult(writer, msg.ID, null)
	default:
		if msg.ID == nil {
			return false, state, nil
		}
		if state == stateUninitialized {
			return false, state, writeError(writer, msg.ID, codeServerNotInitialized, "server not initialized")
		}
		return false, state, writeError(writer, msg.ID, codeMethodNotFound, "method not found")
	}
}

func writeResult(writer *bufio.Writer, id, result json.RawMessage) error {
	return writeResponse(writer, response{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  result,
	})
}

func writeError(writer *bufio.Writer, id json.RawMessage, code int, message string) error {
	return writeResponse(writer, response{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &responseError{
			Code:    code,
			Message: message,
		},
	})
}
