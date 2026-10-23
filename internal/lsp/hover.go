package lsp

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

type documentPositionParams struct {
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

// hover returns schema details for UDS HCL and cross-file definitions for values variables.
func (s *documentStore) hover(ctx context.Context, params documentPositionParams) *hoverResult {
	doc, found := s.document(params.TextDocument.URI)
	if !found {
		return nil
	}
	offset, ok := byteAtPosition(doc.Text, *params.Position)
	if !ok {
		return nil
	}
	definitionFields, _ := definitionFieldsForDocument(doc.URI)
	if definitionFields == nil {
		return s.valuesVariableHover(ctx, doc, *params.Position, offset)
	}
	// Syntax alone supplies structural field locations, even when semantic validation fails.
	file, _ := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
	if file == nil {
		return nil
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	pos := hcl.Pos{Byte: offset}
	if result := s.hclExpressionHover(ctx, doc, body, pos); result != nil {
		return result
	}
	field, sourceRange := definitionFieldAtPos(body, definitionFields, pos)
	if field == nil {
		return nil
	}
	format := hoverContentFormat([]string{s.hoverContentFormat})
	return &hoverResult{
		Contents: markupContent{Kind: format, Value: fieldHoverText(*field, format)},
		Range:    rangeFromHCL(doc.Text, sourceRange),
	}
}

func (s *documentStore) valuesVariableHover(ctx context.Context, doc document, pos position, offset int) *hoverResult {
	names := templateVariableAt(doc.Text, offset)
	if len(names) == 0 {
		return nil
	}
	definitions := s.valuesVariableDefinitions(ctx, doc, pos)
	if len(definitions) == 0 {
		return nil
	}

	path := templateVariableDisplay(names)
	format := hoverContentFormat([]string{s.hoverContentFormat})
	var text strings.Builder
	if format == "markdown" {
		text.WriteString("**" + markdownCodeSpan(path) + "**\n\nDefined in:")
	} else {
		text.WriteString(path + "\n\nDefined in:")
	}
	hasConfig, hasDefaults := false, false
	for _, definition := range definitions {
		name := variableDefinitionName(definition.URI)
		hasConfig = hasConfig || name == bundle.BundleConfigFileName
		hasDefaults = hasDefaults || name == bundle.BundleDefaultsFileName
		if format == "markdown" {
			text.WriteString("\n- " + markdownCodeSpan(name))
		} else {
			text.WriteString("\n- " + name)
		}
	}
	if hasConfig && hasDefaults {
		text.WriteString("\n\nConfig overrides the default value.")
	}
	return &hoverResult{
		Contents: markupContent{Kind: format, Value: text.String()},
		Range:    templateVariableHoverRange(doc.Text, offset, names[len(names)-1]),
	}
}

func templateVariableDisplay(names []string) string {
	var result strings.Builder
	result.WriteString(".vars")
	for i, name := range names {
		if isTemplateIdentifier(name) {
			result.WriteString("." + name)
			continue
		}
		arguments := make([]string, 0, len(names)-i)
		for _, indexedName := range names[i:] {
			arguments = append(arguments, strconv.Quote(indexedName))
		}
		return "index " + result.String() + " " + strings.Join(arguments, " ")
	}
	return result.String()
}

func markdownBacktickFence(value string, minimum int) string {
	length := minimum
	for strings.Contains(value, strings.Repeat("`", length)) {
		length++
	}
	return strings.Repeat("`", length)
}

func markdownCodeSpan(value string) string {
	fence := markdownBacktickFence(value, 1)
	padding := ""
	if strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") || strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") {
		padding = " "
	}
	return fence + padding + value + padding + fence
}

func variableDefinitionName(uri string) string {
	if filename, ok := fileURIPath(uri); ok {
		return filepath.Base(filename)
	}
	return uri
}

func templateVariableHoverRange(text string, offset int, name string) documentRange {
	if quoted, ok := templateQuotedHoverRange(text, offset); ok {
		return quoted
	}
	if offset < len(text) && text[offset] == '.' {
		return documentRange{Start: positionAtByte(text, offset), End: positionAtByte(text, offset+1)}
	}
	if offset > 0 && text[offset-1] == '.' && !isTemplateIdentifierAt(text, offset) {
		return documentRange{Start: positionAtByte(text, offset-1), End: positionAtByte(text, offset)}
	}
	if offset < len(text) && text[offset] == '$' {
		_, end := templateIdentifierBounds(text, offset+1)
		return documentRange{Start: positionAtByte(text, offset), End: positionAtByte(text, end)}
	}
	start, end := templateIdentifierBounds(text, min(offset, len(text)))
	if start > 0 && text[start-1] == '$' {
		start--
		return documentRange{Start: positionAtByte(text, start), End: positionAtByte(text, end)}
	}
	startMin := max(0, offset-len(name))
	startMax := min(offset, len(text)-len(name))
	for start = startMax; start >= startMin; start-- {
		if text[start:start+len(name)] == name && offset <= start+len(name) {
			return documentRange{Start: positionAtByte(text, start), End: positionAtByte(text, start+len(name))}
		}
	}
	pos := positionAtByte(text, offset)
	return documentRange{Start: pos, End: pos}
}

func templateQuotedHoverRange(text string, offset int) (documentRange, bool) {
	end := min(offset+1, len(text))
	open := strings.LastIndex(text[:end], "{{")
	if open < 0 {
		return documentRange{}, false
	}
	quoteStart := -1
	var quote byte
	escaped := false
	for i := open + 2; i < len(text); i++ {
		switch {
		case escaped:
			escaped = false
		case quote == '"' && text[i] == '\\':
			escaped = true
		case quoteStart < 0 && (text[i] == '"' || text[i] == '`'):
			quoteStart, quote = i, text[i]
		case quoteStart >= 0 && text[i] == quote:
			if offset >= quoteStart && offset <= i {
				start, end := quoteStart+1, i
				if start == end {
					start, end = quoteStart, i+1
				}
				return documentRange{Start: positionAtByte(text, start), End: positionAtByte(text, end)}, true
			}
			quoteStart, quote = -1, 0
		case i > offset && quoteStart < 0:
			return documentRange{}, false
		}
	}
	return documentRange{}, false
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
		kind = field.Type + " (repeatable)"
	}
	required := "no"
	if field.Required {
		required = "yes"
	}
	var text strings.Builder
	text.WriteString(heading.String() + "\n\nType: " + kind + "\n\nRequired by parser: " + required)
	text.WriteString(fieldValueVariantsText(field, format))
	text.WriteString(fieldDetailsText(field, format))
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
		return "```hcl\n" + field.Name + " = " + example + "\n```\n\n**" + requirement + "** · Type: `" + field.Type + "` · *Parser schema*" + fieldValueVariantsText(field, "markdown") + fieldDetailsText(field, "markdown")
	}

	kind := "block"
	if field.Repeated {
		kind = "repeatable block"
	}
	var text strings.Builder
	text.WriteString("**`" + heading + "`**\n\n*Parser schema · " + kind + " · " + strings.ToLower(requirement) + "*")
	text.WriteString(fieldDetailsText(field, "markdown"))
	if len(field.Fields) > 0 {
		text.WriteString("\n\n" + markdownFieldTable(field.Fields))
	}
	return text.String()
}

func markdownFieldTable(fields []bundle.DefinitionField) string {
	const requiredWidth = len("Required")
	fieldWidth, typeWidth := len("Field"), len("Type")
	for _, field := range fields {
		fieldWidth = max(fieldWidth, len(field.Name))
		typeWidth = max(typeWidth, len(field.Type))
	}

	var text strings.Builder
	writeRow := func(field, fieldType, required string) {
		if text.Len() > 0 {
			text.WriteByte('\n')
		}
		text.WriteString("| " + field + strings.Repeat(" ", fieldWidth-len(field)))
		text.WriteString(" | " + fieldType + strings.Repeat(" ", typeWidth-len(fieldType)))
		text.WriteString(" | " + required + strings.Repeat(" ", requiredWidth-len(required)) + " |")
	}
	writeRow("Field", "Type", "Required")
	writeRow(strings.Repeat("-", fieldWidth), strings.Repeat("-", typeWidth), strings.Repeat("-", requiredWidth))
	for _, field := range fields {
		required := "No"
		if field.Required {
			required = "Yes"
		}
		writeRow(field.Name, field.Type, required)
	}
	return text.String()
}

func fieldValueVariantsText(field bundle.DefinitionField, format string) string {
	values := fieldSupportedStringValues(field)
	if field.Type == "bool" {
		values = []string{"false", "true"}
	}
	if len(values) == 0 {
		return ""
	}
	if format == "markdown" {
		return "\n\nSupported values: `" + strings.Join(values, "`, `") + "`"
	}
	return "\n\nSupported values: " + strings.Join(values, ", ")
}

type fieldHoverDetails struct {
	description  string
	defaultValue string
	constraint   string
}

var fieldHoverDetailsByName = map[string]fieldHoverDetails{
	"bundle_api_version":     {description: "Selects the UDS bundle schema version."},
	"source":                 {description: "References a local Zarf package archive or an OCI package."},
	"namespace":              {description: "Overrides the package deployment namespace."},
	"depends_on":             {description: "Orders this package after the referenced packages."},
	"values_files":           {description: "Applies YAML values files in list order."},
	"optional_components":    {description: "Selects optional Zarf package components."},
	"options":                {description: "Configures shared UDS CLI behavior."},
	"log_level":              {description: "Controls UDS CLI logging verbosity.", defaultValue: "info"},
	"architecture":           {description: "Selects the target package architecture.", defaultValue: "host architecture"},
	"plain_http":             {description: "Allows plain HTTP for registry operations.", defaultValue: "false"},
	"skip_tls_verify":        {description: "Disables registry TLS certificate verification.", defaultValue: "false"},
	"tmp_dir":                {description: "Selects the directory for temporary files.", defaultValue: "system temporary directory", constraint: "Must name an existing directory when set."},
	"concurrency":            {description: "Limits concurrent registry operations.", defaultValue: "10", constraint: "Zero uses the default; otherwise the value must be between 1 and 25."},
	"variables":              {description: "Defines values available to templates through .vars."},
	"signature_verification": {description: "Configures the signature verification policy."},
	"verify":                 {description: "Enables package signature verification.", defaultValue: "true", constraint: "When false, public_key and keyless must be omitted."},
	"public_key":             {description: "Contains the public key used for signature verification. Use file() to load it from disk.", constraint: "Choose exactly one of public_key or keyless when verification is enabled."},
	"keyless":                {description: "Verifies signatures with certificate identity and OIDC issuer constraints.", constraint: "Choose exactly one identity field and one issuer field."},
	"trusted_root":           {description: "Contains Sigstore trusted-root JSON. Use file() to load it from disk."},
	"insecure_ignore_tlog":   {description: "Skips transparency-log verification.", defaultValue: "false"},
	"insecure_ignore_sct":    {description: "Skips signed-certificate-timestamp verification.", defaultValue: "false"},
	"use_signed_timestamps":  {description: "Requires signed timestamps during keyless verification.", defaultValue: "false"},
}

func fieldDetailsText(field bundle.DefinitionField, format string) string {
	detail := fieldHoverDetailsByName[field.Name]
	var text strings.Builder
	if detail.description != "" {
		text.WriteString("\n\n" + detail.description)
	}
	if detail.defaultValue != "" {
		if format == "markdown" {
			text.WriteString("\n\nDefault: `" + detail.defaultValue + "`")
		} else {
			text.WriteString("\n\nDefault: " + detail.defaultValue)
		}
	}
	if detail.constraint != "" {
		text.WriteString("\n\n" + detail.constraint)
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
