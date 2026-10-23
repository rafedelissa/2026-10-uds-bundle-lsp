package lsp

import (
	"context"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

func (s *documentStore) hclExpressionHover(ctx context.Context, doc document, body *hclsyntax.Body, pos hcl.Pos) *hoverResult {
	if result := s.hclVariableHover(ctx, doc, body, pos); result != nil {
		return result
	}
	if call := fileCallAtPosition(body, pos); call != nil {
		return s.fileCallHover(doc, call)
	}
	if traversal := traversalAtPosition(body, pos); traversal != nil {
		if result := s.traversalHover(ctx, doc, body, traversal, pos); result != nil {
			return result
		}
	}
	if definitions := s.valuesFileDefinitions(ctx, doc, body, pos); len(definitions) > 0 {
		return s.valuesFileHover(doc, body, definitions, pos)
	}
	return s.packageSourceHover(doc, body, pos)
}

func (s *documentStore) traversalHover(ctx context.Context, doc document, body *hclsyntax.Body, traversal hcl.Traversal, pos hcl.Pos) *hoverResult {
	filename, local := fileURIPath(doc.URI)
	if !local || filepath.Base(filename) != bundle.BundleFileName {
		return nil
	}
	namespace, name, ok := definitionReference(traversal)
	if !ok {
		return nil
	}
	var heading, markdownDetail, plainDetail string
	switch namespace {
	case "local":
		attr := localDefinition(body, name)
		if attr == nil {
			return nil
		}
		heading = "local." + name
		source := strings.TrimSpace(sourceForRange(doc.Text, attr.Expr.Range()))
		markdownDetail = "Defined as:\n\n" + markdownHCLBlock(source)
		plainDetail = "Defined as " + source + "."
	case "package":
		block := packageDefinition(body, name)
		if block == nil {
			return nil
		}
		heading = "package." + name
		markdownDetail, plainDetail = "Package reference.", "Package reference."
		if source := block.Body.Attributes["source"]; source != nil {
			expression := strings.TrimSpace(sourceForRange(doc.Text, source.Expr.Range()))
			markdownDetail = "Package source:\n\n" + markdownHCLBlock(expression)
			plainDetail = "Package source: " + expression + "."
		}
	case "sys":
		if name != "arch" {
			return nil
		}
		heading = "sys.arch"
		architecture := s.effectiveArchitecture(ctx, doc)
		markdownDetail = "Effective target architecture: " + markdownCodeSpan(architecture) + "."
		plainDetail = "Effective target architecture: " + architecture + "."
	default:
		return nil
	}
	format := hoverContentFormat([]string{s.hoverContentFormat})
	plain := heading + "\n\n" + plainDetail
	markdown := "**" + markdownCodeSpan(heading) + "**\n\n" + markdownDetail
	sourceRange := traversal.SourceRange()
	for _, traverser := range traversal {
		if traverser.SourceRange().ContainsPos(pos) {
			sourceRange = traverser.SourceRange()
			break
		}
	}
	return hoverWithText(doc.Text, sourceRange, format, markdown, plain)
}

func localDefinition(body *hclsyntax.Body, name string) *hclsyntax.Attribute {
	for _, block := range body.Blocks {
		if block.Type == "locals" {
			if attr := block.Body.Attributes[name]; attr != nil {
				return attr
			}
		}
	}
	return nil
}

func packageDefinition(body *hclsyntax.Body, name string) *hclsyntax.Block {
	for _, block := range body.Blocks {
		if block.Type == "package" && len(block.Labels) > 0 && block.Labels[0] == name {
			return block
		}
	}
	return nil
}

func (s *documentStore) effectiveArchitecture(ctx context.Context, doc document) string {
	filename, ok := fileURIPath(doc.URI)
	if !ok {
		return ""
	}
	_, architecture, _ := s.variablesAtPath(ctx, filepath.Join(filepath.Dir(filename), bundle.BundleConfigFileName), make(map[string][]diagnostic))
	if architecture == "" {
		return runtime.GOARCH
	}
	return architecture
}

func fileCallAtPosition(body *hclsyntax.Body, pos hcl.Pos) *hclsyntax.FunctionCallExpr {
	var result *hclsyntax.FunctionCallExpr
	_ = hclsyntax.VisitAll(body, func(node hclsyntax.Node) hcl.Diagnostics {
		call, ok := node.(*hclsyntax.FunctionCallExpr)
		if ok && call.Name == "file" && call.Range().ContainsPos(pos) {
			result = call
		}
		return nil
	})
	return result
}

func (s *documentStore) fileCallHover(doc document, call *hclsyntax.FunctionCallExpr) *hoverResult {
	format := hoverContentFormat([]string{s.hoverContentFormat})
	markdown := "**`file(path string) string`**\n\nReads a local UTF-8 file relative to the HCL document."
	plain := "file(path string) string\n\nReads a local UTF-8 file relative to the HCL document."
	if len(call.Args) == 1 {
		if value, diagnostics := call.Args[0].Value(nil); !diagnostics.HasErrors() && value.IsKnown() && !value.IsNull() && value.Type() == cty.String {
			display := displayDocumentPath(doc.URI, value.AsString())
			markdown += "\n\nResolved path: " + markdownCodeSpan(display)
			plain += "\n\nResolved path: " + display
		}
	}
	return hoverWithText(doc.Text, call.Range(), format, markdown, plain)
}

func (s *documentStore) valuesFileHover(doc document, body *hclsyntax.Body, definitions []location, pos hcl.Pos) *hoverResult {
	format := hoverContentFormat([]string{s.hoverContentFormat})
	var markdown, plain strings.Builder
	markdown.WriteString("**Values file**\n\nResolved target")
	plain.WriteString("Values file\n\nResolved target")
	if len(definitions) > 1 {
		markdown.WriteString("s")
		plain.WriteString("s")
	}
	markdown.WriteString(":")
	plain.WriteString(":")
	for _, definition := range definitions {
		name := displayTargetPath(doc.URI, definition.URI)
		markdown.WriteString("\n- " + markdownCodeSpan(name))
		plain.WriteString("\n- " + name)
	}
	return hoverWithText(doc.Text, valuesFileExpressionRange(body, pos), format, markdown.String(), plain.String())
}

func valuesFileExpressionRange(body *hclsyntax.Body, pos hcl.Pos) hcl.Range {
	for _, block := range body.Blocks {
		if block.Type != "package" || block.Body.Attributes["values_files"] == nil {
			continue
		}
		attr := block.Body.Attributes["values_files"]
		if expressions, diagnostics := hcl.ExprList(attr.Expr); !diagnostics.HasErrors() {
			for _, expression := range expressions {
				if expression.Range().ContainsPos(pos) {
					return expression.Range()
				}
			}
		}
		if attr.Expr.Range().ContainsPos(pos) {
			return attr.Expr.Range()
		}
	}
	return hcl.Range{Start: pos, End: pos}
}

func displayDocumentPath(documentURI, target string) string {
	if document, ok := fileURIPath(documentURI); ok && !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(document), target)
	}
	if filepath.IsAbs(target) {
		return displayTargetPath(documentURI, localFileURI(target))
	}
	return filepath.ToSlash(target)
}

func displayTargetPath(sourceURI, targetURI string) string {
	target, ok := fileURIPath(targetURI)
	if !ok {
		return targetURI
	}
	if source, ok := fileURIPath(sourceURI); ok {
		if relative, err := filepath.Rel(filepath.Dir(source), target); err == nil {
			return filepath.ToSlash(relative)
		}
	}
	return filepath.ToSlash(target)
}

func (s *documentStore) packageSourceHover(doc document, body *hclsyntax.Body, pos hcl.Pos) *hoverResult {
	for _, block := range body.Blocks {
		if block.Type != "package" {
			continue
		}
		attr := block.Body.Attributes["source"]
		if attr == nil || !attr.Expr.Range().ContainsPos(pos) {
			continue
		}
		format := hoverContentFormat([]string{s.hoverContentFormat})
		markdown, plain := "**Package source**", "Package source"
		value, diagnostics := attr.Expr.Value(nil)
		switch {
		case diagnostics.HasErrors() || !value.IsKnown() || value.IsNull() || value.Type() != cty.String:
			markdown += "\n\nComputed source expression."
			plain += "\n\nComputed source expression."
		case strings.HasPrefix(value.AsString(), "oci://"):
			markdown += "\n\nOCI source: " + markdownCodeSpan(value.AsString())
			plain += "\n\nOCI source: " + value.AsString()
		default:
			display := displayDocumentPath(doc.URI, value.AsString())
			markdown += "\n\nLocal archive: " + markdownCodeSpan(display)
			plain += "\n\nLocal archive: " + display
		}
		return hoverWithText(doc.Text, attr.Expr.Range(), format, markdown, plain)
	}
	return nil
}

func (s *documentStore) hclVariableHover(ctx context.Context, doc document, body *hclsyntax.Body, pos hcl.Pos) *hoverResult {
	filename, ok := fileURIPath(doc.URI)
	if !ok || (filepath.Base(filename) != bundle.BundleConfigFileName && filepath.Base(filename) != bundle.BundleDefaultsFileName) {
		return nil
	}
	variables := body.Attributes["variables"]
	if variables == nil {
		return nil
	}
	names, keyRange, value, ok := variableAtPosition(variables.Expr, pos, nil)
	if !ok {
		return nil
	}

	format := hoverContentFormat([]string{s.hoverContentFormat})
	display := templateVariableDisplay(names)
	source := strings.TrimSpace(sourceForRange(doc.Text, value.Range()))
	valueType := "dynamic"
	if evaluated, ok := typedExpressionValue(value); ok {
		valueType = evaluated.Type().FriendlyName()
	}
	markdown := "**" + markdownCodeSpan(display) + "**\n\nType: " + markdownCodeSpan(valueType) + "\n\n" + markdownHCLBlock(source)
	plain := display + "\n\nType: " + valueType + "\n\n" + source

	dir := filepath.Dir(filename)
	counterpart := bundle.BundleDefaultsFileName
	relation := "Overrides the default value."
	if filepath.Base(filename) == bundle.BundleDefaultsFileName {
		counterpart = bundle.BundleConfigFileName
		relation = "Overridden by config."
	}
	if other, err := s.sourceDocument(filepath.Join(dir, counterpart)); err == nil {
		_, exact := variableDefinitionRange(other, names)
		if exact || variableAncestorOverrides(other, names) {
			markdown += "\n\n" + relation
			plain += "\n\n" + relation
		}
	}
	if references := s.variableReferenceFiles(ctx, dir, names); len(references) > 0 {
		markdownReferences := make([]string, len(references))
		for i, reference := range references {
			markdownReferences[i] = markdownCodeSpan(reference)
		}
		markdown += "\n\nReferenced by: " + strings.Join(markdownReferences, ", ")
		plain += "\n\nReferenced by: " + strings.Join(references, ", ")
	}
	return hoverWithText(doc.Text, keyRange, format, markdown, plain)
}

func variableAtPosition(expr hcl.Expression, pos hcl.Pos, parents []string) ([]string, hcl.Range, hcl.Expression, bool) {
	items, diagnostics := hcl.ExprMap(expr)
	if diagnostics.HasErrors() {
		return nil, hcl.Range{}, nil, false
	}
	for _, item := range items {
		key, diagnostics := item.Key.Value(nil)
		if diagnostics.HasErrors() || !key.IsKnown() || key.IsNull() || key.Type() != cty.String {
			continue
		}
		names := append(slices.Clone(parents), key.AsString())
		if item.Key.Range().ContainsPos(pos) {
			return names, item.Key.Range(), item.Value, true
		}
		if item.Value.Range().ContainsPos(pos) {
			if names, keyRange, value, found := variableAtPosition(item.Value, pos, names); found {
				return names, keyRange, value, true
			}
		}
	}
	return nil, hcl.Range{}, nil, false
}

func (s *documentStore) variableReferenceFiles(ctx context.Context, dir string, names []string) []string {
	cacheKey := dir + "\x00" + templateVariableDisplay(names)
	if result, found := s.variableReferenceCache[cacheKey]; found {
		return slices.Clone(result)
	}
	var result []string
	for bundlePath, filenames := range s.valuesFilesByBundle {
		if ctx.Err() != nil {
			return result
		}
		if filepath.Dir(bundlePath) != dir {
			continue
		}
		for _, filename := range filenames {
			values, err := s.sourceDocument(filename)
			if err == nil && templateReferencesVariable(values.Text, names) {
				name := displayTargetPath(localFileURI(bundlePath), values.URI)
				if !slices.Contains(result, name) {
					result = append(result, name)
				}
			}
		}
	}
	slices.Sort(result)
	s.variableReferenceCache[cacheKey] = slices.Clone(result)
	return result
}

func markdownHCLBlock(source string) string {
	fence := markdownBacktickFence(source, 3)
	var result strings.Builder
	result.Grow(len(source) + len(fence)*2 + len("hcl\n\n"))
	result.WriteString(fence)
	result.WriteString("hcl\n")
	result.WriteString(source)
	result.WriteByte('\n')
	result.WriteString(fence)
	return result.String()
}

func sourceForRange(text string, sourceRange hcl.Range) string {
	start, end := sourceRange.Start.Byte, sourceRange.End.Byte
	if start < 0 || end < start || end > len(text) {
		return ""
	}
	return text[start:end]
}

func hoverWithText(text string, sourceRange hcl.Range, format, markdown, plain string) *hoverResult {
	content := plain
	if format == "markdown" {
		content = markdown
	}
	return &hoverResult{Contents: markupContent{Kind: format, Value: content}, Range: rangeFromHCL(text, sourceRange)}
}
