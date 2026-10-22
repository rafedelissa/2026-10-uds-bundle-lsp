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

	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
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
// streams carries lifecycle logs separately from the protocol output.
func Serve(ctx context.Context, input InterruptibleReader, output io.Writer, streams iostreams.IOStreams) error {
	reader := bufio.NewReaderSize(input, maxHeaderLineSize+1)
	writer := bufio.NewWriter(output)
	state := stateUninitialized
	documents := newDocumentStore()

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

		shouldExit, nextState, err := handleMessage(ctx, writer, msg, state, documents, streams)
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

func handleMessage(ctx context.Context, writer *bufio.Writer, msg message, state serverState, documents *documentStore, streams iostreams.IOStreams) (bool, serverState, error) {
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
		var params initializeParams
		if len(msg.Params) > 0 {
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				return false, state, writeError(writer, msg.ID, codeInvalidParams, "invalid initialize parameters")
			}
		}
		name, version := "unknown", "unknown"
		if params.ClientInfo != nil {
			if params.ClientInfo.Name != "" {
				name = params.ClientInfo.Name
			}
			if params.ClientInfo.Version != "" {
				version = params.ClientInfo.Version
			}
		}
		streams.Info("LSP initialize received", "client_name", name, "client_version", version)
		documents.diagnosticVersionSupport = params.Capabilities.TextDocument.PublishDiagnostics.VersionSupport
		documents.hoverContentFormat = hoverContentFormat(params.Capabilities.TextDocument.Hover.ContentFormat)
		documents.completionSnippetSupport = params.Capabilities.TextDocument.Completion.CompletionItem.SnippetSupport
		documents.completionContentFormat = hoverContentFormat(params.Capabilities.TextDocument.Completion.CompletionItem.DocumentationFormat)
		result, err := json.Marshal(initializeResult{
			Capabilities: serverCapabilities{
				CompletionProvider:        completionOptions{TriggerCharacters: []string{"{"}},
				DefinitionProvider:        true,
				DocumentHighlightProvider: true,
				HoverProvider:             true,
				PositionEncoding:          "utf-16",
				RenameProvider:            true,
				TextDocumentSync: textDocumentSyncOptions{
					OpenClose: true,
					Change:    1, // TextDocumentSyncKind.Full
					Save:      saveOptions{IncludeText: false},
				},
			},
			ServerInfo: serverInfo{Name: "uds-lsp"},
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
	case "textDocument/didOpen":
		return handleDocumentNotification(ctx, writer, msg, state, documents, documents.didOpen)
	case "textDocument/didChange":
		return handleDocumentNotification(ctx, writer, msg, state, documents, documents.didChange)
	case "textDocument/didSave":
		return handleDocumentNotification(ctx, writer, msg, state, documents, documents.didSave)
	case "textDocument/didClose":
		return handleDocumentNotification(ctx, writer, msg, state, documents, documents.didClose)
	case "textDocument/rename":
		if msg.ID == nil {
			return false, state, nil
		}
		if state != stateInitialized {
			return false, state, writeError(writer, msg.ID, codeServerNotInitialized, "server not initialized")
		}
		var params renameParams
		if err := json.Unmarshal(msg.Params, &params); err != nil || params.TextDocument.URI == "" || params.Position == nil || params.Position.Line < 0 || params.Position.Character < 0 {
			return false, state, writeError(writer, msg.ID, codeInvalidParams, "invalid rename parameters")
		}
		edit, err := documents.rename(params)
		if err != nil {
			return false, state, writeError(writer, msg.ID, codeInvalidParams, err.Error())
		}
		result, err := json.Marshal(edit)
		if err != nil {
			return false, state, fmt.Errorf("encode rename result: %w", err)
		}
		return false, state, writeResult(writer, msg.ID, result)
	case "textDocument/hover", "textDocument/completion", "textDocument/definition", "textDocument/documentHighlight":
		if msg.ID == nil {
			return false, state, nil
		}
		if state != stateInitialized {
			return false, state, writeError(writer, msg.ID, codeServerNotInitialized, "server not initialized")
		}
		var params documentPositionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil || params.TextDocument.URI == "" || params.Position == nil || params.Position.Line < 0 || params.Position.Character < 0 {
			return false, state, writeError(writer, msg.ID, codeInvalidParams, "invalid document position parameters")
		}
		var value any
		switch msg.Method {
		case "textDocument/completion":
			value = documents.complete(params)
		case "textDocument/definition":
			value = documents.definition(params)
		case "textDocument/documentHighlight":
			value = documents.highlight(params)
		default:
			value = documents.hover(params)
		}
		result, err := json.Marshal(value)
		if err != nil {
			return false, state, fmt.Errorf("encode %s result: %w", msg.Method, err)
		}
		return false, state, writeResult(writer, msg.ID, result)
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

// handleDocumentNotification enforces the notification-only document synchronization contract.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_synchronization.
func handleDocumentNotification(
	ctx context.Context,
	writer *bufio.Writer,
	msg message,
	state serverState,
	documents *documentStore,
	handle func(json.RawMessage) error,
) (bool, serverState, error) {
	if msg.ID != nil {
		return false, state, writeError(writer, msg.ID, codeInvalidRequest, "text document synchronization method must be a notification")
	}
	if state != stateInitialized || documents == nil {
		return false, state, nil
	}
	if err := handle(msg.Params); err != nil {
		return false, state, fmt.Errorf("handle %s notification: %w", msg.Method, err)
	}
	return false, state, documents.publishDiagnostics(ctx, writer, msg.Params)
}

func writeResult(writer *bufio.Writer, id, result json.RawMessage) error {
	return writeMessage(writer, response{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  result,
	})
}

func writeError(writer *bufio.Writer, id json.RawMessage, code int, message string) error {
	return writeMessage(writer, response{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &responseError{
			Code:    code,
			Message: message,
		},
	})
}
