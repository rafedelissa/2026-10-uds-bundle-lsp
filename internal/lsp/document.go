// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
)

// document is the server's in-memory representation of an open text document.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocumentItem.
type document struct {
	URI        string
	LanguageID string
	Version    int32
	Text       string
}

// documentStore tracks documents covered by text document synchronization.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_synchronization.
type documentStore struct {
	documents                map[string]document
	diagnosticVersionSupport bool
}

// textDocumentItem describes a document when it is opened by the client.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocumentItem.
type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int32  `json:"version"`
	Text       string `json:"text"`
}

// versionedTextDocumentIdentifier identifies a document at a specific version.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#versionedTextDocumentIdentifier.
type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int32  `json:"version"`
}

// textDocumentIdentifier identifies a text document by URI.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocumentIdentifier.
type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

// didOpenTextDocumentParams contains the document opened by the client.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_didOpen.
type didOpenTextDocumentParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

// didChangeTextDocumentParams contains a document version and its content changes.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_didChange.
type didChangeTextDocumentParams struct {
	TextDocument   versionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []textDocumentContentChangeEvent `json:"contentChanges"`
}

// textDocumentContentChangeEvent represents a full or incremental document change.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocumentContentChangeEvent.
type textDocumentContentChangeEvent struct {
	Range json.RawMessage `json:"range,omitempty"`
	Text  string          `json:"text"`
}

// didSaveTextDocumentParams identifies a saved document and optionally includes its text.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_didSave.
type didSaveTextDocumentParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

// didCloseTextDocumentParams identifies a document closed by the client.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_didClose.
type didCloseTextDocumentParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

func newDocumentStore() *documentStore {
	return &documentStore{documents: make(map[string]document)}
}

// didOpen records a document when the client opens it.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_didOpen.
func (s *documentStore) didOpen(params json.RawMessage) error {
	var parsed didOpenTextDocumentParams
	if err := json.Unmarshal(params, &parsed); err != nil {
		return fmt.Errorf("decode didOpen parameters: %w", err)
	}
	if parsed.TextDocument.URI == "" {
		return errors.New("didOpen document URI is required")
	}
	if _, found := s.documents[parsed.TextDocument.URI]; found {
		return fmt.Errorf("didOpen document %q is already open", parsed.TextDocument.URI)
	}

	s.documents[parsed.TextDocument.URI] = document{
		URI:        parsed.TextDocument.URI,
		LanguageID: parsed.TextDocument.LanguageID,
		Version:    parsed.TextDocument.Version,
		Text:       parsed.TextDocument.Text,
	}
	return nil
}

// didChange replaces an open document using full text synchronization.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_didChange.
func (s *documentStore) didChange(params json.RawMessage) error {
	var parsed didChangeTextDocumentParams
	if err := json.Unmarshal(params, &parsed); err != nil {
		return fmt.Errorf("decode didChange parameters: %w", err)
	}
	current, found := s.documents[parsed.TextDocument.URI]
	if !found {
		return fmt.Errorf("didChange document %q is not open", parsed.TextDocument.URI)
	}
	if parsed.TextDocument.Version <= current.Version {
		return nil
	}
	if len(parsed.ContentChanges) == 0 {
		return errors.New("didChange content changes are required")
	}

	for _, change := range parsed.ContentChanges {
		if len(change.Range) > 0 && string(change.Range) != "null" {
			return errors.New("didChange range is not supported with full synchronization")
		}
	}

	current.Version = parsed.TextDocument.Version
	current.Text = parsed.ContentChanges[len(parsed.ContentChanges)-1].Text
	s.documents[current.URI] = current
	return nil
}

// didSave records text included when the client saves an open document.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_didSave.
func (s *documentStore) didSave(params json.RawMessage) error {
	var parsed didSaveTextDocumentParams
	if err := json.Unmarshal(params, &parsed); err != nil {
		return fmt.Errorf("decode didSave parameters: %w", err)
	}
	if parsed.TextDocument.URI == "" {
		return errors.New("didSave document URI is required")
	}
	current, found := s.documents[parsed.TextDocument.URI]
	if !found {
		return fmt.Errorf("didSave document %q is not open", parsed.TextDocument.URI)
	}
	if parsed.Text == nil {
		return nil
	}
	current.Text = *parsed.Text
	s.documents[current.URI] = current
	return nil
}

// didClose removes a document when the client closes it.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_didClose.
func (s *documentStore) didClose(params json.RawMessage) error {
	var parsed didCloseTextDocumentParams
	if err := json.Unmarshal(params, &parsed); err != nil {
		return fmt.Errorf("decode didClose parameters: %w", err)
	}
	if parsed.TextDocument.URI == "" {
		return errors.New("didClose document URI is required")
	}
	if _, found := s.documents[parsed.TextDocument.URI]; !found {
		return fmt.Errorf("didClose document %q is not open", parsed.TextDocument.URI)
	}
	delete(s.documents, parsed.TextDocument.URI)
	return nil
}

func (s *documentStore) document(uri string) (document, bool) {
	doc, found := s.documents[uri]
	return doc, found
}
