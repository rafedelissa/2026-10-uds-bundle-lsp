// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"context"
	"sort"
)

// referencesParams identifies a symbol and whether its declaration should be returned.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#referenceParams.
type referencesParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     *position              `json:"position"`
	Context      referenceContext       `json:"context"`
}

type referenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// references returns locations for a local, package, or values variable.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#textDocument_references.
func (s *documentStore) references(ctx context.Context, params referencesParams) ([]location, error) {
	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return nil, nil
	}

	body, pos, bundleDocument := bundleDocumentContext(doc, *params.Position)
	if !bundleDocument {
		return s.valuesVariableReferences(ctx, doc, *params.Position, params.Context.IncludeDeclaration)
	}

	target, found := findRenameTarget(body, pos)
	if !found {
		return nil, nil
	}
	result := make([]location, 0, len(target.references)+1)
	if params.Context.IncludeDeclaration {
		result = append(result, location{URI: doc.URI, Range: rangeFromHCL(doc.Text, target.declaration)})
	}
	for _, reference := range target.references {
		result = append(result, location{URI: doc.URI, Range: rangeFromHCL(doc.Text, reference)})
	}
	sortLocations(result)
	return result, nil
}

func sortLocations(locations []location) {
	sort.Slice(locations, func(i, j int) bool {
		if locations[i].URI != locations[j].URI {
			return locations[i].URI < locations[j].URI
		}
		return rangeBefore(locations[i].Range, locations[j].Range)
	})
}

func rangeBefore(a, b documentRange) bool {
	if a.Start.Line != b.Start.Line {
		return a.Start.Line < b.Start.Line
	}
	if a.Start.Character != b.Start.Character {
		return a.Start.Character < b.Start.Character
	}
	if a.End.Line != b.End.Line {
		return a.End.Line < b.End.Line
	}
	return a.End.Character < b.End.Character
}
