// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"net/url"
	"path"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// location identifies a symbol definition in a document.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#location.
type location struct {
	URI   string        `json:"uri"`
	Range documentRange `json:"range"`
}

// definition resolves local and package references in an open bundle document.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_definition.
func (s *documentStore) definition(params documentPositionParams) *location {
	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return nil
	}
	body, pos, ok := definitionContext(doc, *params.Position)
	if !ok {
		return nil
	}
	namespace, name, ok := definitionReference(body, pos)
	if !ok {
		return nil
	}
	target, ok := definitionRange(body, namespace, name)
	if !ok {
		return nil
	}
	return &location{URI: doc.URI, Range: rangeFromHCL(doc.Text, target)}
}

func definitionContext(doc document, pos position) (*hclsyntax.Body, hcl.Pos, bool) {
	uri, err := url.Parse(doc.URI)
	if err != nil || path.Base(uri.Path) != bundle.BundleFileName {
		return nil, hcl.Pos{}, false
	}
	offset, ok := byteAtPosition(doc.Text, pos)
	if !ok {
		return nil, hcl.Pos{}, false
	}
	file, diagnostics := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
	if file == nil || (diagnostics.HasErrors() && file.Body == nil) {
		return nil, hcl.Pos{}, false
	}
	body, ok := file.Body.(*hclsyntax.Body)
	return body, hcl.Pos{Byte: offset}, ok
}

func definitionReference(body *hclsyntax.Body, pos hcl.Pos) (string, string, bool) {
	traversal := traversalAtPosition(body, pos)
	if len(traversal) < 2 {
		return "", "", false
	}
	root, rootOK := traversal[0].(hcl.TraverseRoot)
	name, nameOK := traversal[1].(hcl.TraverseAttr)
	if !rootOK || !nameOK {
		return "", "", false
	}
	return root.Name, name.Name, true
}

func definitionRange(body *hclsyntax.Body, namespace, name string) (hcl.Range, bool) {
	switch namespace {
	case "local":
		return localDefinitionRange(body, name)
	case "package":
		return packageDefinitionRange(body, name)
	default:
		return hcl.Range{}, false
	}
}

func traversalAtPosition(body *hclsyntax.Body, pos hcl.Pos) hcl.Traversal {
	for _, attr := range body.Attributes {
		for _, traversal := range attr.Expr.Variables() {
			if traversal.SourceRange().ContainsPos(pos) {
				return traversal
			}
		}
	}
	for _, block := range body.Blocks {
		if traversal := traversalAtPosition(block.Body, pos); traversal != nil {
			return traversal
		}
	}
	return nil
}

func localDefinitionRange(body *hclsyntax.Body, name string) (hcl.Range, bool) {
	for _, block := range body.Blocks {
		if block.Type != "locals" {
			continue
		}
		if attr := block.Body.Attributes[name]; attr != nil {
			return attr.NameRange, true
		}
	}
	return hcl.Range{}, false
}

func packageDefinitionRange(body *hclsyntax.Body, name string) (hcl.Range, bool) {
	for _, block := range body.Blocks {
		if block.Type == "package" && len(block.Labels) > 0 && block.Labels[0] == name && len(block.LabelRanges) > 0 {
			return block.LabelRanges[0], true
		}
	}
	return hcl.Range{}, false
}
