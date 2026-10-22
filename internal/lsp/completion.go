package lsp

import (
	"net/url"
	"path"
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

const (
	insertTextFormatPlainText   = 1
	insertTextFormatSnippet     = 2
	completionItemKindVariable  = 6
	completionItemKindModule    = 9
	completionItemKindProperty  = 10
	completionItemKindSnippet   = 15
	completionItemKindReference = 18
)

func (s *documentStore) complete(params documentPositionParams) completionList {
	result := completionList{Items: make([]completionItem, 0)}
	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return result
	}
	uri, _ := url.Parse(doc.URI)
	if uri == nil || path.Base(uri.Path) != bundle.BundleFileName {
		return result
	}
	offset, ok := byteAtPosition(doc.Text, *params.Position)
	if !ok {
		return result
	}
	start, end, next := completionWord(doc.Text, doc.URI, offset)
	marker := completionMarker(doc.Text)
	if references, found := s.completeReference(doc, start, end, next, marker); found {
		return references
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
	fields, current := completionBody(body, bundle.BundleDefinitionFields(), marker)
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
