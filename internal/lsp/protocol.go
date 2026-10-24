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

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type documentRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

// initializeParams contains client information and capabilities used by this server.
// Unknown capabilities are ignored for forward compatibility.
type initializeParams struct {
	ClientInfo       *clientInfo       `json:"clientInfo,omitempty"`
	RootURI          string            `json:"rootUri"`
	RootPath         string            `json:"rootPath"`
	WorkspaceFolders []workspaceFolder `json:"workspaceFolders"`
	Capabilities     struct {
		Workspace struct {
			DidChangeWatchedFiles struct {
				DynamicRegistration bool `json:"dynamicRegistration"`
			} `json:"didChangeWatchedFiles"`
		} `json:"workspace"`
		TextDocument struct {
			Completion struct {
				CompletionItem struct {
					SnippetSupport      bool     `json:"snippetSupport"`
					DocumentationFormat []string `json:"documentationFormat"`
				} `json:"completionItem"`
			} `json:"completion"`
			Hover struct {
				ContentFormat []string `json:"contentFormat"`
			} `json:"hover"`
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
	CompletionProvider         completionOptions       `json:"completionProvider"`
	DefinitionProvider         bool                    `json:"definitionProvider"`
	DocumentFormattingProvider bool                    `json:"documentFormattingProvider"`
	DocumentHighlightProvider  bool                    `json:"documentHighlightProvider"`
	HoverProvider              bool                    `json:"hoverProvider"`
	PositionEncoding           string                  `json:"positionEncoding"`
	ReferencesProvider         bool                    `json:"referencesProvider"`
	RenameProvider             bool                    `json:"renameProvider"`
	TextDocumentSync           textDocumentSyncOptions `json:"textDocumentSync"`
	Workspace                  workspaceCapabilities   `json:"workspace"`
}

type workspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type workspaceCapabilities struct {
	WorkspaceFolders workspaceFolderCapabilities `json:"workspaceFolders"`
}

type workspaceFolderCapabilities struct {
	Supported           bool `json:"supported"`
	ChangeNotifications bool `json:"changeNotifications"`
}

type changeWorkspaceFoldersParams struct {
	Event struct {
		Added   []workspaceFolder `json:"added"`
		Removed []workspaceFolder `json:"removed"`
	} `json:"event"`
}

type completionOptions struct {
	ResolveProvider   bool     `json:"resolveProvider"`
	TriggerCharacters []string `json:"triggerCharacters"`
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
