// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// formattingParams identifies a document and the client's formatting preferences.
// UDS HCL uses its canonical format rather than client-specific tab settings.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#documentFormattingParams.
type formattingParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Options      formattingOptions      `json:"options"`
}

// formattingOptions contains the required LSP formatting options. Additional
// client properties are ignored because UDS HCL has a canonical style.
type formattingOptions struct {
	TabSize      int  `json:"tabSize"`
	InsertSpaces bool `json:"insertSpaces"`
}

// FormattedDocument contains canonical content for a changed UDS HCL file.
type FormattedDocument struct {
	Path         string
	OriginalText string
	Text         string
}

// Format scans a bundle workspace and returns changed UDS HCL documents without writing them.
func Format(ctx context.Context, root string) ([]FormattedDocument, error) {
	_, store, err := indexBundleWorkspace(ctx, root, "format")
	if err != nil {
		return nil, err
	}

	var result []FormattedDocument
	for _, filename := range slices.Sorted(maps.Keys(store.workspaceFiles)) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		doc, err := store.sourceDocument(filename)
		if err != nil {
			return nil, fmt.Errorf("read %q for formatting: %w", filename, err)
		}
		formatted, changed := formatUDSHCL(doc.Text)
		if changed {
			result = append(result, FormattedDocument{Path: filename, OriginalText: doc.Text, Text: formatted})
		}
	}
	return result, nil
}

// formatDocument returns one full-document edit when canonical formatting changes UDS HCL.
func (s *documentStore) formatDocument(params formattingParams) ([]textEdit, error) {
	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return nil, fmt.Errorf("document %q is not open", params.TextDocument.URI)
	}
	if udsHCLFileName(doc.URI) == "" {
		return []textEdit{}, nil
	}

	formatted, changed := formatUDSHCL(doc.Text)
	if !changed {
		return []textEdit{}, nil
	}
	return []textEdit{{
		Range: documentRange{
			Start: position{},
			End:   positionAtByte(doc.Text, len(doc.Text)),
		},
		NewText: formatted,
	}}, nil
}

func formatUDSHCL(text string) (string, bool) {
	formatted := string(hclwrite.Format([]byte(text)))
	return formatted, formatted != text
}
