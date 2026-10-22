package lsp

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

type completionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []completionItem `json:"items"`
}

type completionItem struct {
	Label            string        `json:"label"`
	Kind             int           `json:"kind"`
	Detail           string        `json:"detail"`
	Documentation    markupContent `json:"documentation"`
	FilterText       string        `json:"filterText"`
	InsertTextFormat int           `json:"insertTextFormat"`
	SortText         string        `json:"sortText,omitempty"`
	TextEdit         textEdit      `json:"textEdit"`
}

type textEdit struct {
	Range   documentRange `json:"range"`
	NewText string        `json:"newText"`
}

type templateCursor struct {
	parentNames []string
	start       int
	end         int
	isNamespace bool
}

const (
	insertTextFormatPlainText   = 1
	insertTextFormatSnippet     = 2
	completionItemKindVariable  = 6
	completionItemKindModule    = 9
	completionItemKindProperty  = 10
	completionItemKindSnippet   = 15
	completionItemKindReference = 18
)

func (s *documentStore) complete(ctx context.Context, params documentPositionParams) completionList {
	result := completionList{Items: make([]completionItem, 0)}
	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return result
	}
	offset, ok := byteAtPosition(doc.Text, *params.Position)
	if !ok {
		return result
	}
	definitionFields, isBundle := definitionFieldsForDocument(doc.URI)
	if definitionFields == nil {
		return s.completeValuesFile(ctx, doc, offset)
	}
	start, end, next := completionWord(doc.Text, doc.URI, offset)
	marker := completionMarker(doc.Text)
	if isBundle {
		if references, found := s.completeReference(doc, start, end, next, marker); found {
			return references
		}
	}

	// Repair only the word being completed in a temporary buffer. The HCL
	// parser can then locate its containing body while a field is half typed.
	// In expressions, strings, and comments the marker is not a body attribute,
	// so those locations naturally produce no field suggestions.
	probe := marker
	if next != hclsyntax.TokenEqual {
		probe += " = null\n"
	}
	file, _ := hclsyntax.ParseConfig([]byte(doc.Text[:start]+probe+doc.Text[end:]), doc.URI, hcl.InitialPos)
	if file == nil {
		return result
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return result
	}
	fields, current := completionBody(body, definitionFields, marker)
	if current == nil {
		return result
	}
	r := documentRange{Start: positionAtByte(doc.Text, start), End: positionAtByte(doc.Text, end)}
	header := next == hclsyntax.TokenOBrace || next == hclsyntax.TokenOQuote
	format := hoverContentFormat([]string{s.completionContentFormat})
	for _, field := range fields {
		isBlock := field.Type == "block"
		if (next == hclsyntax.TokenEqual && isBlock) || (header && !isBlock) {
			continue
		}
		if fieldAlreadyPresent(current, field) {
			continue
		}
		item := completionItem{
			Label: field.Name, FilterText: field.Name, Kind: completionItemKindProperty,
			Detail:        field.Type,
			Documentation: markupContent{Kind: format, Value: fieldHoverText(field, format)},
			TextEdit:      textEdit{Range: r},
		}
		if isBlock {
			item.Kind = completionItemKindSnippet
		}
		if next == hclsyntax.TokenEqual || header {
			// Preserve an assignment or block body that is already present.
			item.TextEdit.NewText, item.InsertTextFormat = field.Name, insertTextFormatPlainText
		} else {
			item.TextEdit.NewText, item.InsertTextFormat = completionInsertion(field, s.completionSnippetSupport)
			if isBlock {
				item.TextEdit.NewText = blockCompletionLines(doc.Text, start, end, item.TextEdit.NewText)
			}
		}
		result.Items = append(result.Items, item)
	}
	return result
}

// completeValuesFile provides .vars path completion in referenced YAML values files.
func (s *documentStore) completeValuesFile(ctx context.Context, doc document, offset int) completionList {
	result := completionList{Items: make([]completionItem, 0)}
	cursor, found := templateCursorAt(doc.Text, offset)
	if !found {
		return result
	}
	variables := s.variablesForValuesFile(ctx, doc.URI)
	if len(variables) == 0 {
		return result
	}

	for _, parent := range cursor.parentNames {
		nested, ok := variables[parent].(bundle.Variables)
		if !ok {
			return result
		}
		variables = nested
	}

	r := documentRange{Start: positionAtByte(doc.Text, cursor.start), End: positionAtByte(doc.Text, cursor.end)}
	format := hoverContentFormat([]string{s.completionContentFormat})
	if cursor.isNamespace {
		newText := "vars."
		if cursor.end < len(doc.Text) && doc.Text[cursor.end] == '.' {
			newText = "vars"
		}
		result.Items = append(result.Items, completionItem{
			Label:            "vars",
			Kind:             completionItemKindModule,
			Detail:           "UDS deploy-time variables",
			Documentation:    markupContent{Kind: format, Value: "UDS deploy-time variables"},
			FilterText:       "vars",
			InsertTextFormat: insertTextFormatPlainText,
			SortText:         completionSortPrefix + "vars",
			TextEdit:         textEdit{Range: r, NewText: newText},
		})
		return result
	}

	names := make([]string, 0, len(variables))
	for name := range variables {
		if isTemplateIdentifier(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		result.Items = append(result.Items, completionItem{
			Label:            name,
			Kind:             completionItemKindProperty,
			Detail:           "deploy-time variable",
			Documentation:    markupContent{Kind: format, Value: "UDS deploy-time variable"},
			FilterText:       name,
			InsertTextFormat: insertTextFormatPlainText,
			SortText:         completionSortPrefix + name,
			TextEdit:         textEdit{Range: r, NewText: name},
		})
	}
	return result
}

func templateCursorAt(text string, offset int) (templateCursor, bool) {
	if offset < 0 || offset > len(text) {
		return templateCursor{}, false
	}
	start, end := offset, offset
	for start > 0 && isTemplateIdentifierByte(text[start-1]) {
		start--
	}
	for end < len(text) && isTemplateIdentifierByte(text[end]) {
		end++
	}

	open := strings.LastIndex(text[:start], "{{")
	if open < 0 || strings.LastIndex(text[:start], "}}") > open {
		return templateCursor{}, false
	}
	pathStart := start
	for pathStart > open+2 && (text[pathStart-1] == '.' || isTemplateIdentifierByte(text[pathStart-1])) {
		pathStart--
	}
	prefix := text[pathStart:start]
	if prefix == "." {
		return templateCursor{start: start, end: end, isNamespace: true}, true
	}
	if !strings.HasPrefix(prefix, ".vars.") {
		return templateCursor{}, false
	}

	parentPath := strings.TrimSuffix(prefix[len(".vars."):], ".")
	var parents []string
	if parentPath != "" {
		parents = strings.Split(parentPath, ".")
		for _, parent := range parents {
			if parent == "" {
				return templateCursor{}, false
			}
		}
	}
	return templateCursor{parentNames: parents, start: start, end: end}, true
}

func isTemplateIdentifier(name string) bool {
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		return false
	}
	for i := range len(name) {
		if !isTemplateIdentifierByte(name[i]) {
			return false
		}
	}
	return true
}

func isTemplateIdentifierByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

// HCL requires nested blocks on their own lines. Account for completion just
// after an opening brace, or before a closing brace on the same line.
func blockCompletionLines(text string, start, end int, insertion string) string {
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	prefix := text[lineStart:start]
	indent := prefix[:len(prefix)-len(strings.TrimLeft(prefix, " \t"))]
	if strings.TrimSpace(prefix) != "" {
		childIndent := indent + "  "
		insertion = "\n" + childIndent + strings.ReplaceAll(insertion, "\n", "\n"+childIndent)
	}
	suffix, _, _ := strings.Cut(text[end:], "\n")
	if strings.TrimSpace(suffix) != "" {
		insertion += "\n" + indent
	}
	return insertion
}

func completionMarker(text string) string {
	var marker strings.Builder
	marker.WriteString("_uds_completion")
	for strings.Contains(text, marker.String()) {
		marker.WriteByte('_')
	}
	return marker.String()
}

// completionWord includes the whole identifier so completion in the middle of
// a word replaces its suffix as well as its prefix. Other tokens are preserved.
func completionWord(text, filename string, offset int) (int, int, hclsyntax.TokenType) {
	tokens, _ := hclsyntax.LexConfig([]byte(text), filename, hcl.InitialPos)
	start, end := offset, offset
	for _, token := range tokens {
		if token.Type == hclsyntax.TokenIdent && token.Range.Start.Byte <= offset && token.Range.End.Byte >= offset {
			start, end = token.Range.Start.Byte, token.Range.End.Byte
			break
		}
	}
	for _, token := range tokens {
		if token.Range.Start.Byte >= end {
			return start, end, token.Type
		}
	}
	return start, end, hclsyntax.TokenEOF
}

func completionBody(body *hclsyntax.Body, fields []bundle.DefinitionField, marker string) ([]bundle.DefinitionField, *hclsyntax.Body) {
	if body == nil {
		return nil, nil
	}
	if body.Attributes[marker] != nil {
		return fields, body
	}
	for _, block := range body.Blocks {
		var nested []bundle.DefinitionField
		for _, field := range fields {
			if field.Type == "block" && field.Name == block.Type {
				nested = field.Fields
				break
			}
		}
		if childFields, child := completionBody(block.Body, nested, marker); child != nil {
			return childFields, child
		}
	}
	return nil, nil
}

func fieldAlreadyPresent(body *hclsyntax.Body, field bundle.DefinitionField) bool {
	if field.Type != "block" {
		return body.Attributes[field.Name] != nil
	}
	if field.Repeated {
		return false
	}
	for _, block := range body.Blocks {
		if block.Type == field.Name {
			return true
		}
	}
	return false
}

func completionInsertion(field bundle.DefinitionField, snippets bool) (string, int) {
	if field.Type == "block" {
		var text strings.Builder
		text.WriteString(field.Name)
		for i, label := range field.Labels {
			value := label
			if snippets {
				value = "${" + strconv.Itoa(i+1) + ":" + label + "}"
			}
			// Quote HCL labels without escaping snippet placeholder characters.
			text.WriteString(" \"" + value + "\"")
		}
		if snippets {
			text.WriteString(" {\n  $0\n}")
			return text.String(), insertTextFormatSnippet
		}
		text.WriteString(" {\n  \n}")
		return text.String(), insertTextFormatPlainText
	}
	value := "{}"
	if snippets {
		switch {
		case field.Type == "string":
			value = `"${1}"`
		case field.Type == "bool":
			value = "${1|true,false|}"
		case field.Type == "number":
			value = "${1:0}"
		case field.Name == "depends_on":
			value = "[package.${1:name}]"
		case strings.HasPrefix(field.Type, "list("):
			value = "[${1}]"
		default:
			value = "{${1}}"
		}
		return field.Name + " = " + value + "$0", insertTextFormatSnippet
	}
	switch {
	case field.Type == "string":
		value = `""`
	case field.Type == "bool":
		value = "false"
	case field.Type == "number":
		value = "0"
	case strings.HasPrefix(field.Type, "list("):
		value = "[]"
	}
	return field.Name + " = " + value, insertTextFormatPlainText
}
