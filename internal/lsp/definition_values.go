package lsp

import (
	"context"
	"net/url"
	"path/filepath"
	"slices"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

func (s *documentStore) valuesFileDefinitions(ctx context.Context, doc document, body *hclsyntax.Body, pos hcl.Pos) []location {
	filename, ok := fileURIPath(doc.URI)
	if !ok {
		return nil
	}
	for _, block := range body.Blocks {
		if block.Type != "package" || len(block.Labels) != 1 {
			continue
		}
		attr := block.Body.Attributes["values_files"]
		if attr == nil || !attr.Expr.Range().ContainsPos(pos) {
			continue
		}
		_, arch, _ := s.variablesAtPath(ctx, filepath.Join(filepath.Dir(filename), bundle.BundleConfigFileName), make(map[string][]diagnostic))
		uri, err := url.Parse(doc.URI)
		if err != nil {
			return nil
		}
		parsed, _ := parseBundleDocument(ctx, doc, uri, arch)
		if parsed == nil {
			return nil
		}
		// A literal list identifies one file. A computed list expression can
		// identify several files, so return every resolved target in that case.
		index := -1
		if expressions, diags := hcl.ExprList(attr.Expr); !diags.HasErrors() {
			for i, expr := range expressions {
				if expr.Range().ContainsPos(pos) {
					index = i
					break
				}
			}
			if index < 0 {
				return nil
			}
		}
		var result []location
		for _, pkg := range parsed.Packages {
			if pkg.Name != block.Labels[0] {
				continue
			}
			for i, valuesPath := range pkg.ValuesFiles {
				if ctx.Err() != nil {
					return result
				}
				if index >= 0 && index != i {
					continue
				}
				if !filepath.IsAbs(valuesPath) {
					valuesPath = filepath.Join(filepath.Dir(filename), valuesPath)
				}
				if target, err := s.sourceDocument(valuesPath); err == nil {
					result = appendDefinition(result, location{URI: target.URI})
				}
			}
		}
		return result
	}
	return nil
}

func (s *documentStore) valuesVariableDefinitions(ctx context.Context, doc document, pos position) []location {
	filename, ok := fileURIPath(doc.URI)
	if !ok || (filepath.Ext(filename) != ".yaml" && filepath.Ext(filename) != ".yml") {
		return nil
	}
	offset, ok := byteAtPosition(doc.Text, pos)
	if !ok {
		return nil
	}
	names := templateVariableAt(doc.Text, offset)
	if len(names) == 0 {
		return nil
	}
	var result []location
	// Share diagnostics' complete, deterministic discovery, buffer precedence,
	// and architecture context rather than selecting an arbitrary open bundle.
	for _, candidate := range s.diagnosticBundles(ctx) {
		if ctx.Err() != nil {
			break
		}
		bundlePath, ok := fileURIPath(candidate.URI)
		if !ok {
			continue
		}
		dir := filepath.Dir(bundlePath)
		_, arch, _ := s.variablesAtPath(ctx, filepath.Join(dir, bundle.BundleConfigFileName), make(map[string][]diagnostic))
		uri, err := url.Parse(candidate.URI)
		if err != nil {
			continue
		}
		parsed, _ := parseBundleDocument(ctx, candidate, uri, arch)
		if parsed == nil || !referencesValuesFile(parsed, bundlePath, filename) {
			continue
		}
		for _, name := range []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName} {
			varsDoc, err := s.sourceDocument(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			if target, ok := variableDefinitionRange(varsDoc, names); ok {
				result = appendDefinition(result, location{URI: varsDoc.URI, Range: rangeFromHCL(varsDoc.Text, target)})
			}
		}
	}
	return result
}

func appendDefinition(locations []location, target location) []location {
	if slices.Contains(locations, target) {
		return locations
	}
	return append(locations, target)
}

func variableDefinitionRange(doc document, names []string) (hcl.Range, bool) {
	file, _ := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
	if file == nil {
		return hcl.Range{}, false
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok || body.Attributes["variables"] == nil {
		return hcl.Range{}, false
	}
	return objectDefinitionRange(body.Attributes["variables"].Expr, names)
}

func objectDefinitionRange(expr hcl.Expression, names []string) (hcl.Range, bool) {
	items, diags := hcl.ExprMap(expr)
	if !diags.HasErrors() {
		for _, item := range items {
			key, diags := item.Key.Value(nil)
			if diags.HasErrors() || !key.IsKnown() || key.IsNull() || key.Type() != cty.String || key.AsString() != names[0] {
				continue
			}
			if len(names) == 1 {
				return item.Key.Range(), true
			}
			return objectDefinitionRange(item.Value, names[1:])
		}
		return hcl.Range{}, false
	}
	// Computed objects have no literal key range. If a constant expression
	// supplies the requested path, navigate to that defining expression.
	value, diags := expr.Value(nil)
	for _, name := range names {
		if diags.HasErrors() || !value.IsKnown() || value.IsNull() || !value.Type().IsObjectType() || !value.Type().HasAttribute(name) {
			return hcl.Range{}, false
		}
		value = value.GetAttr(name)
	}
	return expr.Range(), true
}
