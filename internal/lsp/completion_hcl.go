package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const (
	localCompletionNamespace   = "local"
	packageCompletionNamespace = "package"
	systemCompletionNamespace  = "sys"
	completionSortPrefix       = "0000_"
)

type hclStringCursor struct {
	field             bundle.DefinitionField
	start             int
	end               int
	prefix            string
	needsClosingQuote bool
	isFileArgument    bool
}

type pathCompletionKind int

const (
	pathCompletionNone pathCompletionKind = iota
	pathCompletionFile
	pathCompletionDirectory
	pathCompletionSource
	pathCompletionValues
)

// completeHCLString provides enum and local path completion inside HCL strings.
func (s *documentStore) completeHCLString(doc document, fields []bundle.DefinitionField, offset int) (completionList, bool) {
	cursor, found := hclStringCursorAt(doc, fields, offset)
	if !found {
		return completionList{Items: make([]completionItem, 0)}, false
	}

	if cursor.isFileArgument {
		return s.pathCompletions(doc, cursor, pathCompletionFile), true
	}
	if kind := pathCompletionForField(cursor.field); kind != pathCompletionNone {
		return s.pathCompletions(doc, cursor, kind), true
	}
	values := scalarCompletionValues(cursor.field)
	if len(values) == 0 {
		return completionList{Items: make([]completionItem, 0)}, false
	}
	return s.stringCompletions(doc, cursor, values), true
}

// completeHCLExpression provides references, built-ins, and scalar values in HCL expressions.
func (s *documentStore) completeHCLExpression(doc document, fields []bundle.DefinitionField, isBundle bool, start, end int, next hclsyntax.TokenType, marker string) (completionList, bool) {
	result := completionList{Items: make([]completionItem, 0)}

	var namespace string
	switch {
	case strings.HasSuffix(doc.Text[:start], localCompletionNamespace+"."):
		namespace = localCompletionNamespace
	case strings.HasSuffix(doc.Text[:start], packageCompletionNamespace+"."):
		namespace = packageCompletionNamespace
	case strings.HasSuffix(doc.Text[:start], systemCompletionNamespace+"."):
		namespace = systemCompletionNamespace
	}

	file, _ := hclsyntax.ParseConfig([]byte(doc.Text[:start]+marker+doc.Text[end:]), doc.URI, hcl.InitialPos)
	if file == nil {
		return result, namespace != ""
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return result, namespace != ""
	}

	if namespace != "" {
		field, found := completionExpressionField(body, fields, namespace, marker)
		if !found {
			return result, true
		}
		switch namespace {
		case localCompletionNamespace:
			if field.Name == "depends_on" {
				return result, true
			}
		case systemCompletionNamespace:
			if !isBundle || !fieldAcceptsString(field) {
				return result, true
			}
			return s.systemCompletions(doc, start, end), true
		case packageCompletionNamespace:
			if field.Name != "depends_on" {
				return result, true
			}
		}
		return s.referenceCompletions(doc, body, namespace, start, end), true
	}

	field, found := completionExpressionField(body, fields, marker, "")
	if !found {
		return result, false
	}
	return s.expressionCompletions(doc, body, field, isBundle, start, end, next), true
}

func hclStringCursorAt(doc document, fields []bundle.DefinitionField, offset int) (hclStringCursor, bool) {
	file, _ := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
	if file == nil {
		return hclStringCursor{}, false
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return hclStringCursor{}, false
	}
	field, expressionRange, found := completionFieldAtOffset(body, fields, offset)
	if !found {
		return hclStringCursor{}, false
	}

	quoteStart := -1
	escaped := false
	for i := expressionRange.Start.Byte; i < offset && i < len(doc.Text); i++ {
		switch {
		case escaped:
			escaped = false
		case doc.Text[i] == '\\':
			escaped = true
		case doc.Text[i] == '"':
			if quoteStart == -1 {
				quoteStart = i
			} else {
				quoteStart = -1
			}
		}
	}
	if quoteStart == -1 {
		return hclStringCursor{}, false
	}
	content := doc.Text[quoteStart+1 : offset]
	if strings.LastIndex(content, "${") > strings.LastIndex(content, "}") {
		return hclStringCursor{}, false
	}

	cursor := hclStringCursor{
		field:             field,
		start:             quoteStart + 1,
		end:               offset,
		prefix:            content,
		needsClosingQuote: true,
		isFileArgument:    strings.HasSuffix(strings.TrimSpace(doc.Text[expressionRange.Start.Byte:quoteStart]), "file("),
	}
	for end := offset; end < len(doc.Text); end++ {
		switch {
		case escaped:
			escaped = false
		case doc.Text[end] == '\\':
			escaped = true
		case doc.Text[end] == '"':
			cursor.end = end
			cursor.needsClosingQuote = false
			return cursor, true
		case doc.Text[end] == '\n':
			cursor.end = end
			return cursor, true
		}
	}
	cursor.end = len(doc.Text)
	return cursor, true
}

func completionFieldAtOffset(body *hclsyntax.Body, fields []bundle.DefinitionField, offset int) (bundle.DefinitionField, hcl.Range, bool) {
	for name, attribute := range body.Attributes {
		r := attribute.Expr.Range()
		if r.Start.Byte <= offset && offset <= r.End.Byte {
			if field, found := completionField(fields, name); found {
				return field, r, true
			}
		}
	}
	for _, block := range body.Blocks {
		for _, field := range fields {
			if field.Type == "block" && field.Name == block.Type {
				if foundField, r, found := completionFieldAtOffset(block.Body, field.Fields, offset); found {
					return foundField, r, true
				}
			}
		}
	}
	return bundle.DefinitionField{}, hcl.Range{}, false
}

func scalarCompletionValues(field bundle.DefinitionField) []string {
	if field.Type == "dynamic" {
		return nil
	}
	switch field.Name {
	case "bundle_api_version":
		return []string{"uds.dev/v1alpha1"}
	case "log_level":
		return []string{"debug", "error", "info", "warn", "warning"}
	case "architecture":
		return []string{"amd64", "arm64"}
	default:
		return nil
	}
}

func pathCompletionForField(field bundle.DefinitionField) pathCompletionKind {
	if field.Type == "dynamic" {
		return pathCompletionNone
	}
	switch field.Name {
	case "source":
		return pathCompletionSource
	case "values_files":
		return pathCompletionValues
	case "tmp_dir":
		return pathCompletionDirectory
	case "public_key", "trusted_root":
		return pathCompletionFile
	default:
		return pathCompletionNone
	}
}

func (s *documentStore) stringCompletions(doc document, cursor hclStringCursor, values []string) completionList {
	format := hoverContentFormat([]string{s.completionContentFormat})
	r := documentRange{Start: positionAtByte(doc.Text, cursor.start), End: positionAtByte(doc.Text, cursor.end)}
	result := completionList{Items: make([]completionItem, 0, len(values))}
	for _, value := range values {
		if cursor.prefix != "" && !strings.HasPrefix(value, cursor.prefix) {
			continue
		}
		newText := value
		if cursor.needsClosingQuote {
			newText = value + "\""
		}
		result.Items = append(result.Items, completionItem{
			Label: value, Kind: completionItemKindValue, Detail: cursor.field.Name,
			Documentation: markupContent{Kind: format, Value: cursor.field.Name + " value"},
			FilterText:    value, InsertTextFormat: insertTextFormatPlainText,
			SortText: completionSortPrefix + value, TextEdit: textEdit{Range: r, NewText: newText},
		})
	}
	return result
}

func (s *documentStore) pathCompletions(doc document, cursor hclStringCursor, kind pathCompletionKind) completionList {
	result := completionList{Items: make([]completionItem, 0)}
	if strings.Contains(cursor.prefix, "://") {
		return result
	}
	documentPath, ok := fileURIPath(doc.URI)
	if !ok {
		return result
	}

	dirPart, namePrefix := "", cursor.prefix
	if slash := strings.LastIndex(cursor.prefix, "/"); slash >= 0 {
		dirPart, namePrefix = cursor.prefix[:slash+1], cursor.prefix[slash+1:]
	}
	searchDir := filepath.FromSlash(dirPart)
	if !filepath.IsAbs(searchDir) {
		searchDir = filepath.Join(filepath.Dir(documentPath), searchDir)
	}
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return result
	}

	r := documentRange{Start: positionAtByte(doc.Text, cursor.start), End: positionAtByte(doc.Text, cursor.end)}
	format := hoverContentFormat([]string{s.completionContentFormat})
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), namePrefix) || !pathEntryAllowed(entry, kind) {
			continue
		}
		isDir := entry.IsDir()
		suffix := ""
		itemKind := completionItemKindFile
		if isDir {
			suffix = "/"
			itemKind = completionItemKindFolder
		}
		candidate := dirPart + entry.Name() + suffix
		quoted := strconv.Quote(candidate)
		newText := quoted[1 : len(quoted)-1]
		if !isDir && cursor.needsClosingQuote {
			newText = quoted[1:]
		}
		result.Items = append(result.Items, completionItem{
			Label: candidate, Kind: itemKind, Detail: "local path",
			Documentation: markupContent{Kind: format, Value: "local path"}, FilterText: candidate,
			InsertTextFormat: insertTextFormatPlainText, SortText: completionSortPrefix + candidate,
			TextEdit: textEdit{Range: r, NewText: newText},
		})
	}
	return result
}

func pathEntryAllowed(entry os.DirEntry, kind pathCompletionKind) bool {
	if entry.IsDir() {
		return true
	}
	switch kind {
	case pathCompletionFile:
		return true
	case pathCompletionSource:
		return strings.HasSuffix(entry.Name(), ".tar.zst")
	case pathCompletionValues:
		return strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml")
	default:
		return false
	}
}

func completionField(fields []bundle.DefinitionField, name string) (bundle.DefinitionField, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	if len(fields) == 0 {
		return bundle.DefinitionField{Name: name, Type: "dynamic"}, true
	}
	return bundle.DefinitionField{}, false
}

// completionExpressionField finds the schema field containing root or root.member.
// An empty member matches any traversal with that root.
func completionExpressionField(body *hclsyntax.Body, fields []bundle.DefinitionField, root, member string) (bundle.DefinitionField, bool) {
	for name, attribute := range body.Attributes {
		if expressionHasTraversal(attribute.Expr, root, member) {
			if field, found := completionField(fields, name); found {
				return field, true
			}
		}
	}
	for _, block := range body.Blocks {
		for _, field := range fields {
			if field.Type == "block" && field.Name == block.Type {
				if foundField, found := completionExpressionField(block.Body, field.Fields, root, member); found {
					return foundField, true
				}
			}
		}
	}
	return bundle.DefinitionField{}, false
}

func expressionHasTraversal(expression hclsyntax.Expression, rootName, memberName string) bool {
	for _, traversal := range expression.Variables() {
		if len(traversal) == 0 {
			continue
		}
		root, ok := traversal[0].(hcl.TraverseRoot)
		if !ok || root.Name != rootName {
			continue
		}
		if memberName == "" {
			return true
		}
		if len(traversal) > 1 {
			if member, ok := traversal[1].(hcl.TraverseAttr); ok && member.Name == memberName {
				return true
			}
		}
	}
	return false
}

func (s *documentStore) expressionCompletions(doc document, body *hclsyntax.Body, field bundle.DefinitionField, isBundle bool, start, end int, next hclsyntax.TokenType) completionList {
	prefix := doc.Text[start:end]
	r := documentRange{Start: positionAtByte(doc.Text, start), End: positionAtByte(doc.Text, end)}
	format := hoverContentFormat([]string{s.completionContentFormat})
	result := completionList{Items: make([]completionItem, 0)}
	appendItem := func(label, detail, newText string, kind, insertFormat int) {
		if prefix != "" && !strings.HasPrefix(label, prefix) {
			return
		}
		result.Items = append(result.Items, completionItem{
			Label: label, Kind: kind, Detail: detail,
			Documentation: markupContent{Kind: format, Value: detail}, FilterText: label,
			InsertTextFormat: insertFormat, SortText: completionSortPrefix + label,
			TextEdit: textEdit{Range: r, NewText: newText},
		})
	}

	if field.Name == "depends_on" {
		if len(completionReferenceNames(body, packageCompletionNamespace)) > 0 {
			appendItem(packageCompletionNamespace, "UDS package reference", namespaceInsertion(packageCompletionNamespace, next), completionItemKindModule, insertTextFormatPlainText)
		}
		return result
	}
	if len(completionReferenceNames(body, localCompletionNamespace)) > 0 {
		appendItem(localCompletionNamespace, "UDS local reference", namespaceInsertion(localCompletionNamespace, next), completionItemKindModule, insertTextFormatPlainText)
	}
	if isBundle && fieldAcceptsString(field) {
		appendItem(systemCompletionNamespace, "UDS system values", namespaceInsertion(systemCompletionNamespace, next), completionItemKindModule, insertTextFormatPlainText)
	}
	if _, isFileDocument := fileURIPath(doc.URI); fieldAcceptsString(field) && isFileDocument {
		newText, insertFormat := "file(\"\")", insertTextFormatPlainText
		if next == hclsyntax.TokenOParen {
			newText = "file"
		} else if s.completionSnippetSupport {
			newText, insertFormat = `file("${1:path}")`, insertTextFormatSnippet
		}
		appendItem("file", "read a local UTF-8 file", newText, completionItemKindFunction, insertFormat)
	}
	if field.Type == "bool" {
		appendItem("false", "boolean value", "false", completionItemKindKeyword, insertTextFormatPlainText)
		appendItem("true", "boolean value", "true", completionItemKindKeyword, insertTextFormatPlainText)
	}
	for _, value := range scalarCompletionValues(field) {
		appendItem(value, field.Name+" value", strconv.Quote(value), completionItemKindValue, insertTextFormatPlainText)
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].Label < result.Items[j].Label })
	return result
}

func namespaceInsertion(namespace string, next hclsyntax.TokenType) string {
	if next == hclsyntax.TokenDot {
		return namespace
	}
	return namespace + "."
}

func fieldAcceptsString(field bundle.DefinitionField) bool {
	return field.Type == "string" || field.Type == "list(string)" || field.Type == "dynamic" || field.Name == "variables"
}

func (s *documentStore) systemCompletions(doc document, start, end int) completionList {
	format := hoverContentFormat([]string{s.completionContentFormat})
	return completionList{Items: []completionItem{{
		Label: "arch", Kind: completionItemKindProperty, Detail: "target architecture",
		Documentation: markupContent{Kind: format, Value: "effective target architecture"},
		FilterText:    "arch", InsertTextFormat: insertTextFormatPlainText,
		SortText: completionSortPrefix + "arch",
		TextEdit: textEdit{Range: documentRange{Start: positionAtByte(doc.Text, start), End: positionAtByte(doc.Text, end)}, NewText: "arch"},
	}}}
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
