package lsp

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

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
	parentNames       []string
	start             int
	end               int
	isNamespace       bool
	isIndexKey        bool
	needsClosingQuote bool
}

const (
	insertTextFormatPlainText   = 1
	insertTextFormatSnippet     = 2
	completionItemKindFunction  = 3
	completionItemKindVariable  = 6
	completionItemKindModule    = 9
	completionItemKindProperty  = 10
	completionItemKindValue     = 12
	completionItemKindKeyword   = 14
	completionItemKindSnippet   = 15
	completionItemKindFile      = 17
	completionItemKindReference = 18
	completionItemKindFolder    = 19
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
	if values, found := s.completeHCLString(doc, definitionFields, offset); found {
		return values
	}
	start, end, next := completionWord(doc.Text, doc.URI, offset)
	marker := completionMarker(doc.Text)
	if values, found := s.completeHCLExpression(doc, definitionFields, isBundle, start, end, next, marker); found {
		return values
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
		if cursor.isIndexKey || isTemplateIdentifier(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		newText := name
		if cursor.isIndexKey {
			quoted := strconv.Quote(name)
			end := len(quoted) - 1
			if cursor.needsClosingQuote {
				end++
			}
			newText = quoted[1:end]
		}
		result.Items = append(result.Items, completionItem{
			Label:            name,
			Kind:             completionItemKindProperty,
			Detail:           "deploy-time variable",
			Documentation:    markupContent{Kind: format, Value: "UDS deploy-time variable"},
			FilterText:       name,
			InsertTextFormat: insertTextFormatPlainText,
			SortText:         completionSortPrefix + name,
			TextEdit:         textEdit{Range: r, NewText: newText},
		})
	}
	return result
}

func templateCursorAt(text string, offset int) (templateCursor, bool) {
	if offset < 0 || offset > len(text) {
		return templateCursor{}, false
	}
	open := strings.LastIndex(text[:offset], "{{")
	if open < 0 || strings.LastIndex(text[:offset], "}}") > open {
		return templateCursor{}, false
	}
	if cursor, found := templateIndexCursorAt(text, open, offset); found {
		return cursor, true
	}

	start, end := templateIdentifierBounds(text, offset)

	pathStart := start
	for pathStart > open+2 {
		if text[pathStart-1] == '.' {
			pathStart--
			continue
		}
		r, size := utf8.DecodeLastRuneInString(text[:pathStart])
		if !isTemplateIdentifierRune(r) {
			break
		}
		pathStart -= size
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

func templateIndexCursorAt(text string, open, offset int) (templateCursor, bool) {
	quoteStart := -1
	escaped := false
	for i := open + 2; i < offset; i++ {
		switch {
		case escaped:
			escaped = false
		case text[i] == '\\':
			escaped = true
		case text[i] == '"':
			if quoteStart == -1 {
				quoteStart = i
			} else {
				quoteStart = -1
			}
		}
	}
	if quoteStart == -1 {
		return templateCursor{}, false
	}

	prefix := strings.TrimSpace(text[open+2 : quoteStart])
	prefix = strings.TrimSpace(strings.TrimPrefix(prefix, "-"))
	parts := strings.Fields(prefix)
	if len(parts) != 2 || parts[0] != "index" {
		return templateCursor{}, false
	}

	var parents []string
	switch {
	case parts[1] == ".vars":
	case strings.HasPrefix(parts[1], ".vars."):
		parents = strings.Split(parts[1][len(".vars."):], ".")
		for _, parent := range parents {
			if !isTemplateIdentifier(parent) {
				return templateCursor{}, false
			}
		}
	default:
		return templateCursor{}, false
	}

	cursor := templateCursor{
		parentNames:       parents,
		start:             quoteStart + 1,
		end:               offset,
		isIndexKey:        true,
		needsClosingQuote: true,
	}
	escaped = false
	for end := offset; end < len(text); end++ {
		switch {
		case escaped:
			escaped = false
		case text[end] == '\\':
			escaped = true
		case text[end] == '"':
			cursor.end = end
			cursor.needsClosingQuote = false
			return cursor, true
		case text[end] == '\n' || end+1 < len(text) && text[end:end+2] == "}}":
			cursor.end = end
			return cursor, true
		}
	}
	cursor.end = len(text)
	return cursor, true
}

func isTemplateIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if !isTemplateIdentifierRune(r) || i == 0 && unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isTemplateIdentifierRune(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func isTemplateIdentifierAt(text string, offset int) bool {
	if offset < 0 || offset >= len(text) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text[offset:])
	return isTemplateIdentifierRune(r)
}

func templateIdentifierBounds(text string, offset int) (int, int) {
	start, end := offset, offset
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if !isTemplateIdentifierRune(r) {
			break
		}
		start -= size
	}
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if !isTemplateIdentifierRune(r) {
			break
		}
		end += size
	}
	return start, end
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
