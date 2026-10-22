// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const (
	documentHighlightKindRead  = 2
	documentHighlightKindWrite = 3
)

// documentHighlight identifies a symbol occurrence in a document.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#documentHighlight.
type documentHighlight struct {
	Range documentRange `json:"range"`
	Kind  int           `json:"kind"`
}

// highlight returns local or package declarations and references in an open bundle document.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_documentHighlight.
func (s *documentStore) highlight(params documentPositionParams) []documentHighlight {
	result := make([]documentHighlight, 0)
	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return result
	}
	body, pos, ok := bundleDocumentContext(doc, *params.Position)
	if !ok {
		return result
	}
	namespace, name, ok := definitionReference(traversalAtPosition(body, pos))
	if !ok {
		namespace, name, ok = declarationAtPosition(body, pos)
	}
	if !ok || (namespace != "local" && namespace != "package") {
		return result
	}
	if target, found := definitionRange(body, namespace, name); found {
		result = append(result, documentHighlight{
			Range: rangeFromHCL(doc.Text, target),
			Kind:  documentHighlightKindWrite,
		})
	}
	for _, r := range referenceRanges(body, namespace, name) {
		result = append(result, documentHighlight{
			Range: rangeFromHCL(doc.Text, r),
			Kind:  documentHighlightKindRead,
		})
	}
	return result
}

func declarationAtPosition(body *hclsyntax.Body, pos hcl.Pos) (string, string, bool) {
	for _, block := range body.Blocks {
		switch block.Type {
		case "locals":
			for name, attr := range block.Body.Attributes {
				if attr.NameRange.ContainsPos(pos) {
					return "local", name, true
				}
			}
		case "package":
			if len(block.Labels) > 0 && len(block.LabelRanges) > 0 && block.LabelRanges[0].ContainsPos(pos) {
				return "package", block.Labels[0], true
			}
		}
	}
	return "", "", false
}

func referenceRanges(body *hclsyntax.Body, namespace, name string) []hcl.Range {
	result := make([]hcl.Range, 0)
	for _, attr := range body.Attributes {
		for _, traversal := range attr.Expr.Variables() {
			root, reference, ok := definitionReference(traversal)
			if ok && root == namespace && reference == name {
				result = append(result, hcl.RangeBetween(traversal[0].SourceRange(), traversal[1].SourceRange()))
			}
		}
	}
	for _, block := range body.Blocks {
		result = append(result, referenceRanges(block.Body, namespace, name)...)
	}
	return result
}
