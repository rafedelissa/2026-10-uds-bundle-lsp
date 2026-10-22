// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import "encoding/json"

const (
	jsonRPCVersion = "2.0"

	codeParseError           = -32700
	codeInvalidRequest       = -32600
	codeInvalidParams        = -32602
	codeMethodNotFound       = -32601
	codeServerNotInitialized = -32002
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

// initializeParams contains client information and capabilities used by this server.
// Unknown capabilities are ignored for forward compatibility.
type initializeParams struct {
	ClientInfo   *clientInfo `json:"clientInfo,omitempty"`
	Capabilities struct {
		TextDocument struct {
			PublishDiagnostics struct {
				VersionSupport bool `json:"versionSupport"`
			} `json:"publishDiagnostics"`
		} `json:"textDocument"`
	} `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type serverCapabilities struct {
	PositionEncoding string                  `json:"positionEncoding"`
	TextDocumentSync textDocumentSyncOptions `json:"textDocumentSync"`
}

type textDocumentSyncOptions struct {
	OpenClose bool        `json:"openClose"`
	Change    int         `json:"change"`
	Save      saveOptions `json:"save"`
}

type saveOptions struct {
	IncludeText bool `json:"includeText"`
}

type serverInfo struct {
	Name string `json:"name"`
}
