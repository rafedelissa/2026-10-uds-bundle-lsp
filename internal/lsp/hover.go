package lsp

import (
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

type hoverParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     *position              `json:"position"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type hoverResult struct {
	Contents markupContent `json:"contents"`
	Range    documentRange `json:"range"`
}

func (s *documentStore) hover(params hoverParams) *hoverResult {
	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return nil
	}
	uri, _ := url.Parse(doc.URI)
	if uri == nil || path.Base(uri.Path) != bundle.BundleFileName {
		return nil
	}
	offset, ok := byteAtPosition(doc.Text, *params.Position)
	if !ok {
		return nil
	}
	// Syntax alone supplies field locations, even when semantic validation fails.
	// Hover never evaluates expressions or file() calls.
	file, _ := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
	if file == nil {
		return nil
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	field, sourceRange := definitionFieldAtPos(body, bundle.BundleDefinitionFields(), hcl.Pos{Byte: offset})
	if field == nil {
		return nil
	}
	format := hoverContentFormat([]string{s.hoverContentFormat})
	return &hoverResult{
		Contents: markupContent{Kind: format, Value: fieldHoverText(*field, format)},
		Range:    rangeFromHCL(doc.Text, sourceRange),
	}
}

func definitionFieldAtPos(body *hclsyntax.Body, fields []bundle.DefinitionField, pos hcl.Pos) (*bundle.DefinitionField, hcl.Range) {
	if body == nil {
		return nil, hcl.Range{}
	}
	for _, attr := range body.Attributes {
		if !attr.NameRange.ContainsPos(pos) {
			continue
		}
		for i := range fields {
			if fields[i].Name == attr.Name && fields[i].Type != "block" {
				return &fields[i], attr.NameRange
			}
		}
	}
	for _, block := range body.Blocks {
		for i := range fields {
			field := &fields[i]
			if field.Name != block.Type || field.Type != "block" {
				continue
			}
			if block.TypeRange.ContainsPos(pos) {
				return field, block.TypeRange
			}
			for _, labelRange := range block.LabelRanges {
				if labelRange.ContainsPos(pos) {
					return field, labelRange
				}
			}
			if nested, r := definitionFieldAtPos(block.Body, field.Fields, pos); nested != nil {
				return nested, r
			}
		}
	}
	return nil, hcl.Range{}
}

func fieldHoverText(field bundle.DefinitionField, format string) string {
	var heading strings.Builder
	heading.WriteString(field.Name)
	for _, label := range field.Labels {
		heading.WriteString(" " + strconv.Quote("<"+label+">"))
	}
	if format == "markdown" {
		return markdownFieldHover(field, heading.String())
	}
	kind := field.Type
	if field.Repeated {
		kind += " (repeatable)"
	}
	required := "no"
	if field.Required {
		required = "yes"
	}
	var text strings.Builder
	text.WriteString(heading.String() + "\n\nType: " + kind + "\n\nRequired by parser: " + required)
	if len(field.Fields) > 0 {
		text.WriteString("\n\nFields:")
		for _, child := range field.Fields {
			requirement := "optional"
			if child.Required {
				requirement = "required"
			}
			text.WriteString("\n- " + child.Name + ": " + child.Type + " (" + requirement + ")")
		}
	}
	return text.String()
}

func markdownFieldHover(field bundle.DefinitionField, heading string) string {
	requirement := "Optional"
	if field.Required {
		requirement = "Required"
	}
	if field.Type != "block" {
		example := "{}"
		switch {
		case field.Type == "string":
			example = `"..."`
		case field.Type == "bool":
			example = "false"
		case field.Type == "number":
			example = "0"
		case strings.HasPrefix(field.Type, "list("):
			example = "[]"
		}
		return "```hcl\n" + field.Name + " = " + example + "\n```\n\n**" + requirement + "** · Type: `" + field.Type + "` · *Parser schema*"
	}

	kind := "block"
	if field.Repeated {
		kind = "repeatable block"
	}
	var text strings.Builder
	text.WriteString("**`" + heading + "`**\n\n*Parser schema · " + kind + " · " + strings.ToLower(requirement) + "*")
	if len(field.Fields) > 0 {
		text.WriteString("\n\n| Field | Type | Required |\n| --- | --- | --- |")
		for _, child := range field.Fields {
			required := "No"
			if child.Required {
				required = "Yes"
			}
			text.WriteString("\n| `" + child.Name + "` | `" + child.Type + "` | " + required + " |")
		}
	}
	return text.String()
}

func hoverContentFormat(formats []string) string {
	for _, format := range formats {
		if format == "markdown" || format == "plaintext" {
			return format
		}
	}
	return "plaintext"
}

// byteAtPosition reverses positionAtByte for UTF-16 positions sent by the client.
// Characters beyond the line are clamped to its end, as required by LSP.
func byteAtPosition(text string, pos position) (int, bool) {
	if pos.Line < 0 || pos.Character < 0 {
		return 0, false
	}
	start := 0
	for range pos.Line {
		next := strings.IndexByte(text[start:], '\n')
		if next < 0 {
			return 0, false
		}
		start += next + 1
	}
	end := len(text)
	if next := strings.IndexByte(text[start:], '\n'); next >= 0 {
		end = start + next
	}
	if end > start && text[end-1] == '\r' {
		end--
	}
	units := 0
	for offset := start; offset < end; {
		if units == pos.Character {
			return offset, true
		}
		r, size := utf8.DecodeRuneInString(text[offset:end])
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > pos.Character {
			return 0, false // The position splits a UTF-16 surrogate pair.
		}
		units += width
		offset += size
	}
	return end, true
}
