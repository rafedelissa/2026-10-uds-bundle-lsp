// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// renameParams identifies the symbol and replacement name for a rename request.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#renameParams.
type renameParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     *position              `json:"position"`
	NewName      string                 `json:"newName"`
}

// workspaceEdit describes edits grouped by document URI.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#workspaceEdit.
type workspaceEdit struct {
	Changes map[string][]textEdit `json:"changes"`
}

type renameTarget struct {
	namespace   string
	name        string
	declaration hcl.Range
	references  []hcl.Range
}

// rename returns edits for a local, package, or values variable and all of its references.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_rename.
func (s *documentStore) rename(ctx context.Context, params renameParams) (*workspaceEdit, error) {
	if !hclsyntax.ValidIdentifier(params.NewName) {
		return nil, fmt.Errorf("new name %q is not a valid HCL identifier", params.NewName)
	}

	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return nil, fmt.Errorf("document %q is not open", params.TextDocument.URI)
	}
	if edit, variable, err := s.renameVariable(ctx, doc, *params.Position, params.NewName); variable {
		return edit, err
	}
	body, pos, ok := bundleDocumentContext(doc, *params.Position)
	if !ok {
		return nil, errors.New("cannot rename at the requested document position")
	}

	target, found := findRenameTarget(body, pos)
	if !found {
		return nil, errors.New("no renameable symbol at the requested position")
	}
	if params.NewName != target.name {
		if _, exists := definitionRange(body, target.namespace, params.NewName); exists {
			return nil, fmt.Errorf("%s %q is already defined", target.namespace, params.NewName)
		}
	}

	declarationText := params.NewName
	if target.namespace == "package" {
		declarationText = strconv.Quote(params.NewName)
	}
	edits := make([]textEdit, 0, len(target.references)+1)
	edits = append(edits, textEdit{
		Range:   rangeFromHCL(doc.Text, target.declaration),
		NewText: declarationText,
	})
	for _, reference := range target.references {
		edits = append(edits, textEdit{
			Range:   rangeFromHCL(doc.Text, reference),
			NewText: params.NewName,
		})
	}

	return &workspaceEdit{Changes: map[string][]textEdit{doc.URI: edits}}, nil
}

func findRenameTarget(body *hclsyntax.Body, pos hcl.Pos) (renameTarget, bool) {
	target, found := renameTargetAtPosition(body, pos)
	if found || pos.Byte == 0 {
		return target, found
	}
	// HCL ranges exclude their end offset, but an editor caret immediately
	// after an identifier is also a valid place to invoke rename. Range lookup
	// uses byte offsets, so the last byte also works for Unicode identifiers.
	pos.Byte--
	return renameTargetAtPosition(body, pos)
}

func renameTargetAtPosition(body *hclsyntax.Body, pos hcl.Pos) (renameTarget, bool) {
	namespace, name, found := definitionReference(traversalAtPosition(body, pos))
	if !found {
		namespace, name, found = declarationAtPosition(body, pos)
	}
	if !found || (namespace != "local" && namespace != "package") {
		return renameTarget{}, false
	}

	declaration, found := definitionRange(body, namespace, name)
	if !found {
		return renameTarget{}, false
	}
	return renameTarget{
		namespace:   namespace,
		name:        name,
		declaration: declaration,
		references:  referenceIdentifierRanges(body, namespace, name),
	}, true
}

func referenceIdentifierRanges(body *hclsyntax.Body, namespace, name string) []hcl.Range {
	result := make([]hcl.Range, 0)
	for _, attr := range body.Attributes {
		for _, traversal := range attr.Expr.Variables() {
			root, reference, ok := definitionReference(traversal)
			if !ok || root != namespace || reference != name {
				continue
			}
			r := traversal[1].SourceRange()
			r.Start.Byte++
			r.Start.Column++
			result = append(result, r)
		}
	}
	for _, block := range body.Blocks {
		result = append(result, referenceIdentifierRanges(block.Body, namespace, name)...)
	}
	return result
}
