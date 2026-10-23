// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"fmt"

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

// formatDocument returns one full-document edit when canonical formatting changes UDS HCL.
func (s *documentStore) formatDocument(params formattingParams) ([]textEdit, error) {
	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return nil, fmt.Errorf("document %q is not open", params.TextDocument.URI)
	}
	if udsHCLFileName(doc.URI) == "" {
		return []textEdit{}, nil
	}

	formatted := string(hclwrite.Format([]byte(doc.Text)))
	if formatted == doc.Text {
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
