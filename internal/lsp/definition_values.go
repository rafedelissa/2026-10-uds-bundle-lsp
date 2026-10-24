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
	"github.com/zclconf/go-cty/cty/function"
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
					result = appendLocation(result, location{URI: target.URI})
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
		config, configErr := s.sourceDocument(filepath.Join(dir, bundle.BundleConfigFileName))
		if configErr == nil {
			if target, found := variableDefinitionRange(config, names); found {
				result = appendLocation(result, location{URI: config.URI, Range: rangeFromHCL(config.Text, target)})
			} else if variableAncestorOverrides(config, names) {
				continue // A config ancestor replaces this complete defaults subtree.
			}
		}
		defaults, err := s.sourceDocument(filepath.Join(dir, bundle.BundleDefaultsFileName))
		if err == nil {
			if target, ok := variableDefinitionRange(defaults, names); ok {
				result = appendLocation(result, location{URI: defaults.URI, Range: rangeFromHCL(defaults.Text, target)})
			}
		}
	}
	return result
}

func appendLocation(locations []location, target location) []location {
	if slices.Contains(locations, target) {
		return locations
	}
	return append(locations, target)
}

func variableAncestorOverrides(doc document, names []string) bool {
	if len(names) == 0 {
		return false
	}
	file, _ := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
	if file == nil {
		return false
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok || body.Attributes["variables"] == nil {
		return false
	}
	return objectAncestorOverrides(body.Attributes["variables"].Expr, names)
}

// variableTypeEvalContext classifies file() as a string without reading from disk.
var variableTypeEvalContext = &hcl.EvalContext{Functions: map[string]function.Function{
	"file": function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(_ []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.UnknownVal(cty.String), nil
		},
	}),
}}

func objectAncestorOverrides(expr hcl.Expression, names []string) bool {
	items, diagnostics := hcl.ExprMap(expr)
	if diagnostics.HasErrors() {
		value, ok := typedExpressionValue(expr)
		return ok && objectValueAncestorOverrides(value, names)
	}
	for _, item := range items {
		key, diagnostics := item.Key.Value(nil)
		if diagnostics.HasErrors() || !key.IsKnown() || key.IsNull() || key.Type() != cty.String || key.AsString() != names[0] {
			continue
		}
		if len(names) == 1 {
			return false
		}
		if _, diagnostics := hcl.ExprMap(item.Value); diagnostics.HasErrors() {
			value, ok := typedExpressionValue(item.Value)
			if !ok {
				return false
			}
			if value.Type().IsObjectType() {
				return objectValueAncestorOverrides(value, names[1:])
			}
			return true // A known non-object ancestor replaces the complete default subtree.
		}
		return objectAncestorOverrides(item.Value, names[1:])
	}
	return false
}

func typedExpressionValue(expr hcl.Expression) (cty.Value, bool) {
	value, diagnostics := expr.Value(variableTypeEvalContext)
	return value, !diagnostics.HasErrors() && !value.IsNull() && value.Type() != cty.DynamicPseudoType
}

func objectValueAncestorOverrides(value cty.Value, names []string) bool {
	if !value.Type().IsObjectType() || !value.Type().HasAttribute(names[0]) {
		return false
	}
	if len(names) == 1 {
		return false
	}
	child := value.GetAttr(names[0])
	if child.IsNull() || child.Type() == cty.DynamicPseudoType {
		return false
	}
	if !child.Type().IsObjectType() {
		return true
	}
	return objectValueAncestorOverrides(child, names[1:])
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
	// Computed objects have no literal key range. If the expression's static
	// type supplies the requested path, navigate to the complete expression.
	value, ok := typedExpressionValue(expr)
	for _, name := range names {
		if !ok || value.IsNull() || !value.Type().IsObjectType() || !value.Type().HasAttribute(name) {
			return hcl.Range{}, false
		}
		value = value.GetAttr(name)
	}
	return expr.Range(), true
}
