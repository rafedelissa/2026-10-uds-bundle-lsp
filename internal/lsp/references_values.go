// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"context"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/defenseunicorns/uds-cli/pkg/bundle/spec"
)

func (s *documentStore) valuesVariableReferences(
	ctx context.Context,
	doc document,
	pos position,
	includeDeclaration bool,
) ([]location, error) {
	filename, ok := fileURIPath(doc.URI)
	if !ok {
		return nil, nil
	}

	var names []string
	var owners []valuesReferenceBundle
	switch filepath.Base(filename) {
	case bundle.BundleConfigFileName, bundle.BundleDefaultsFileName:
		var found bool
		names, found = variableNamesAtPosition(doc, pos)
		if !found {
			return nil, nil
		}
		owners = []valuesReferenceBundle{s.valuesReferenceBundle(ctx, filepath.Dir(filename))}
	case bundle.BundleFileName:
		return nil, nil
	default:
		if filepath.Ext(filename) != ".yaml" && filepath.Ext(filename) != ".yml" {
			return nil, nil
		}
		offset, found := byteAtPosition(doc.Text, pos)
		if !found {
			return nil, nil
		}
		names = templateVariableAt(doc.Text, offset)
		if len(names) == 0 {
			return nil, nil
		}
		owners = s.valuesReferenceBundles(ctx, filename)
		if len(owners) == 0 && ctx.Err() == nil {
			return nil, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sourceDir := filepath.Dir(filename)
	sourceIsDefaults := filepath.Base(filename) == bundle.BundleDefaultsFileName
	var result []location
	for _, owner := range owners {
		config, configLocation, configFound := s.variableReferenceDefinition(filepath.Join(owner.dir, bundle.BundleConfigFileName), names)
		_, defaultsLocation, defaultsFound := s.variableReferenceDefinition(filepath.Join(owner.dir, bundle.BundleDefaultsFileName), names)
		if !configFound && config.URI != "" && variableAncestorOverrides(config, names) {
			// The source declaration remains navigable even though config hides it.
			if includeDeclaration && sourceIsDefaults && sourceDir == owner.dir && defaultsFound {
				result = appendLocation(result, defaultsLocation)
			}
			continue
		}

		if includeDeclaration {
			if configFound {
				result = appendLocation(result, configLocation)
			}
			if defaultsFound {
				result = appendLocation(result, defaultsLocation)
			}
		}
		for _, values := range s.valuesDocumentsForReferences(ctx, owner) {
			for _, referenceRange := range templateVariableReferenceRanges(values.Text, names) {
				result = appendLocation(result, location{URI: values.URI, Range: referenceRange})
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	sortLocations(result)
	return result, nil
}

func (s *documentStore) variableReferenceDefinition(filename string, names []string) (document, location, bool) {
	doc, err := s.sourceDocument(filename)
	if err != nil {
		return document{}, location{}, false
	}
	target, found := variableDefinitionRange(doc, names)
	if !found {
		return doc, location{}, false
	}
	return doc, location{URI: doc.URI, Range: rangeFromHCL(doc.Text, target)}, true
}

type valuesReferenceBundle struct {
	dir    string
	parsed *spec.UDSBundle
}

// valuesReferenceBundles skips invalid bundles so unrelated edits do not break lookup.
func (s *documentStore) valuesReferenceBundles(ctx context.Context, filename string) []valuesReferenceBundle {
	var result []valuesReferenceBundle
	for _, candidate := range s.diagnosticBundles(ctx) {
		if ctx.Err() != nil {
			return result
		}
		bundlePath, ok := fileURIPath(candidate.URI)
		if !ok {
			continue
		}
		parsed := s.parseBundleForReferences(ctx, candidate, bundlePath)
		if parsed == nil || !referencesValuesFile(parsed, bundlePath, filename) {
			continue
		}
		dir := filepath.Dir(bundlePath)
		if !slices.ContainsFunc(result, func(existing valuesReferenceBundle) bool { return existing.dir == dir }) {
			result = append(result, valuesReferenceBundle{dir: dir, parsed: parsed})
		}
	}
	slices.SortFunc(result, func(a, b valuesReferenceBundle) int { return strings.Compare(a.dir, b.dir) })
	return result
}

func (s *documentStore) valuesReferenceBundle(ctx context.Context, dir string) valuesReferenceBundle {
	result := valuesReferenceBundle{dir: dir}
	bundlePath := filepath.Join(dir, bundle.BundleFileName)
	doc, err := s.sourceDocument(bundlePath)
	if err == nil {
		result.parsed = s.parseBundleForReferences(ctx, doc, bundlePath)
	}
	return result
}

func (s *documentStore) valuesDocumentsForReferences(ctx context.Context, owner valuesReferenceBundle) []document {
	if owner.parsed == nil {
		return nil
	}
	var result []document
	for _, pkg := range owner.parsed.Packages {
		for _, name := range pkg.ValuesFiles {
			if ctx.Err() != nil {
				return result
			}
			if !filepath.IsAbs(name) {
				name = filepath.Join(owner.dir, name)
			}
			values, err := s.sourceDocument(name)
			if err != nil || slices.ContainsFunc(result, func(existing document) bool { return existing.URI == values.URI }) {
				continue
			}
			result = append(result, values)
		}
	}
	slices.SortFunc(result, func(a, b document) int { return strings.Compare(a.URI, b.URI) })
	return result
}

func (s *documentStore) parseBundleForReferences(ctx context.Context, doc document, filename string) *spec.UDSBundle {
	uri, err := url.Parse(doc.URI)
	if err != nil {
		return nil
	}
	_, architecture, _ := s.variablesAtPath(ctx, filepath.Join(filepath.Dir(filename), bundle.BundleConfigFileName), make(map[string][]diagnostic))
	parsed, _ := parseBundleDocument(ctx, doc, uri, architecture)
	return parsed
}
