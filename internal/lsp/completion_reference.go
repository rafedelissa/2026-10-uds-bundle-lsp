package lsp

import (
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const (
	localCompletionNamespace   = "local"
	packageCompletionNamespace = "package"
	completionSortPrefix       = "0000_"
)

func (s *documentStore) completeReference(doc document, start, end int, next hclsyntax.TokenType, marker string) (completionList, bool) {
	result := completionList{Items: make([]completionItem, 0)}

	var namespace string
	switch {
	case strings.HasSuffix(doc.Text[:start], localCompletionNamespace+"."):
		namespace = localCompletionNamespace
	case strings.HasSuffix(doc.Text[:start], packageCompletionNamespace+"."):
		namespace = packageCompletionNamespace
	}

	file, diagnostics := hclsyntax.ParseConfig([]byte(doc.Text[:start]+marker+doc.Text[end:]), doc.URI, hcl.InitialPos)
	if file == nil || (diagnostics.HasErrors() && file.Body == nil) {
		return result, namespace != ""
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return result, namespace != ""
	}

	if namespace != "" {
		if !hasCompletionReference(body, namespace, marker) {
			return result, true
		}
		return s.referenceCompletions(doc, body, namespace, start, end), true
	}

	attribute, found := completionExpressionAttribute(body, marker)
	if !found {
		return result, false
	}
	return s.namespaceCompletions(doc, body, attribute, start, end, next == hclsyntax.TokenDot), true
}

func completionExpressionAttribute(body *hclsyntax.Body, marker string) (string, bool) {
	for name, attr := range body.Attributes {
		for _, traversal := range attr.Expr.Variables() {
			if len(traversal) == 0 {
				continue
			}
			root, ok := traversal[0].(hcl.TraverseRoot)
			if ok && root.Name == marker {
				return name, true
			}
		}
	}
	for _, block := range body.Blocks {
		if name, found := completionExpressionAttribute(block.Body, marker); found {
			return name, true
		}
	}
	return "", false
}

func (s *documentStore) namespaceCompletions(doc document, body *hclsyntax.Body, attribute string, start, end int, hasDot bool) completionList {
	namespace := localCompletionNamespace
	if attribute == "depends_on" {
		namespace = packageCompletionNamespace
	}
	if len(completionReferenceNames(body, namespace)) == 0 {
		return completionList{Items: make([]completionItem, 0)}
	}

	newText := namespace
	if !hasDot {
		newText = namespace + "."
	}
	format := hoverContentFormat([]string{s.completionContentFormat})
	item := completionItem{
		Label:            namespace,
		Kind:             completionItemKindModule,
		Detail:           "UDS expression namespace",
		Documentation:    markupContent{Kind: format, Value: "UDS " + namespace + " reference"},
		FilterText:       namespace,
		InsertTextFormat: insertTextFormatPlainText,
		SortText:         completionSortPrefix + namespace,
		TextEdit: textEdit{
			Range:   documentRange{Start: positionAtByte(doc.Text, start), End: positionAtByte(doc.Text, end)},
			NewText: newText,
		},
	}
	return completionList{Items: []completionItem{item}}
}

func (s *documentStore) referenceCompletions(doc document, body *hclsyntax.Body, namespace string, start, end int) completionList {
	kind, detail := completionItemKindVariable, "local variable"
	if namespace == packageCompletionNamespace {
		kind, detail = completionItemKindReference, "package"
	}

	format := hoverContentFormat([]string{s.completionContentFormat})
	r := documentRange{Start: positionAtByte(doc.Text, start), End: positionAtByte(doc.Text, end)}
	names := completionReferenceNames(body, namespace)
	result := completionList{Items: make([]completionItem, 0, len(names))}
	for _, name := range names {
		documentation := detail + " " + name
		if format == "markdown" {
			documentation = detail + " `" + name + "`"
		}
		result.Items = append(result.Items, completionItem{
			Label:            name,
			Kind:             kind,
			Detail:           detail,
			Documentation:    markupContent{Kind: format, Value: documentation},
			FilterText:       name,
			InsertTextFormat: insertTextFormatPlainText,
			SortText:         completionSortPrefix + name,
			TextEdit:         textEdit{Range: r, NewText: name},
		})
	}
	return result
}

func completionReferenceNames(body *hclsyntax.Body, namespace string) []string {
	unique := make(map[string]struct{})
	for _, block := range body.Blocks {
		switch {
		case namespace == localCompletionNamespace && block.Type == "locals":
			for name := range block.Body.Attributes {
				unique[name] = struct{}{}
			}
		case namespace == packageCompletionNamespace && block.Type == "package" && len(block.Labels) > 0:
			if hclsyntax.ValidIdentifier(block.Labels[0]) {
				unique[block.Labels[0]] = struct{}{}
			}
		}
	}

	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func hasCompletionReference(body *hclsyntax.Body, namespace, marker string) bool {
	for _, attr := range body.Attributes {
		for _, traversal := range attr.Expr.Variables() {
			root, name, ok := definitionReference(traversal)
			if ok && root == namespace && name == marker {
				return true
			}
		}
	}
	for _, block := range body.Blocks {
		if hasCompletionReference(block.Body, namespace, marker) {
			return true
		}
	}
	return false
}
