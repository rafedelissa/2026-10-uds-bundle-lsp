package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

type variableRenameTarget struct {
	dir        string
	names      []string
	fromValues bool
}

func (s *documentStore) renameVariable(ctx context.Context, doc document, pos position, newName string) (*workspaceEdit, bool, error) {
	target, found, err := s.variableRenameTarget(ctx, doc, pos)
	if err != nil || !found {
		return nil, found, err
	}
	if !isTemplateIdentifier(newName) {
		return nil, true, fmt.Errorf("new variable name %q is not valid in template dot notation", newName)
	}

	definitions, err := s.variableRenameDocuments(ctx, target.dir)
	if err != nil {
		return nil, true, err
	}
	if target.fromValues {
		for _, definition := range definitions {
			if documentBaseName(definition) != bundle.BundleConfigFileName {
				continue
			}
			if !hasVariableDefinition(definition, target.names) && variableAncestorOverrides(definition, target.names) {
				return nil, true, fmt.Errorf("variable %s is replaced by a config ancestor", templateVariableDisplay(target.names))
			}
		}
	}

	for _, definition := range definitions {
		if hasVariableDefinition(definition, target.names) && !hasLiteralVariableKey(definition, target.names) {
			return nil, true, fmt.Errorf("variable %s has no literal key in %s", templateVariableDisplay(target.names), documentBaseName(definition))
		}
	}

	replacementPath := slices.Clone(target.names)
	replacementPath[len(replacementPath)-1] = newName
	if newName != target.names[len(target.names)-1] {
		for _, definition := range definitions {
			if hasVariableDefinition(definition, replacementPath) {
				return nil, true, fmt.Errorf("variable %s is already defined in %s", templateVariableDisplay(replacementPath), documentBaseName(definition))
			}
		}
	}

	changes := make(map[string][]textEdit)
	for _, definition := range definitions {
		keyRange, exists := variableKeyRange(definition, target.names)
		if !exists {
			continue
		}
		changes[definition.URI] = append(changes[definition.URI], textEdit{
			Range:   rangeFromHCL(definition.Text, keyRange),
			NewText: newName,
		})
	}
	if len(changes) == 0 {
		return nil, true, fmt.Errorf("variable %s has no literal definition", templateVariableDisplay(target.names))
	}

	valuesDocuments, err := s.valuesDocumentsForBundle(ctx, target.dir)
	if err != nil {
		return nil, true, err
	}
	for _, values := range valuesDocuments {
		edits := templateVariableRenameEdits(values.Text, target.names, newName)
		if len(edits) == 0 {
			continue
		}
		filename, local := fileURIPath(values.URI)
		if local {
			dirs, discoveryErr := s.bundleDirectoriesForValuesFile(ctx, filename)
			if discoveryErr != nil {
				return nil, true, discoveryErr
			}
			if len(dirs) > 1 {
				return nil, true, fmt.Errorf("values file %q is referenced by multiple bundles; rename is ambiguous", filename)
			}
		}
		changes[values.URI] = edits
	}
	return &workspaceEdit{Changes: changes}, true, nil
}

func (s *documentStore) variableRenameDocuments(ctx context.Context, dir string) ([]document, error) {
	var result []document
	for _, name := range []string{bundle.BundleConfigFileName, bundle.BundleDefaultsFileName} {
		doc, err := s.sourceDocument(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s for rename: %w", name, err)
		}
		switch name {
		case bundle.BundleConfigFileName:
			_, err = bundle.NewHCLParser("", iostreams.IOStreams{}).ParseBundleConfigSource(ctx, []byte(doc.Text), filepath.Join(dir, name))
		default:
			_, err = bundle.ParseDefaultsSource(ctx, []byte(doc.Text), filepath.Join(dir, name))
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s for rename: %w", name, err)
		}
		result = append(result, doc)
	}
	return result, nil
}

func (s *documentStore) variableRenameTarget(ctx context.Context, doc document, pos position) (variableRenameTarget, bool, error) {
	filename, ok := fileURIPath(doc.URI)
	if !ok {
		return variableRenameTarget{}, false, nil
	}
	switch filepath.Base(filename) {
	case bundle.BundleConfigFileName, bundle.BundleDefaultsFileName:
		offset, ok := byteAtPosition(doc.Text, pos)
		if !ok {
			return variableRenameTarget{}, false, nil
		}
		file, _ := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
		if file == nil {
			return variableRenameTarget{}, false, nil
		}
		body, ok := file.Body.(*hclsyntax.Body)
		if !ok || body.Attributes["variables"] == nil {
			return variableRenameTarget{}, false, nil
		}
		variablePos := hcl.Pos{Byte: offset}
		names, _, _, found := variableAtPosition(body.Attributes["variables"].Expr, variablePos, nil)
		if !found && variablePos.Byte > 0 {
			variablePos.Byte--
			names, _, _, found = variableAtPosition(body.Attributes["variables"].Expr, variablePos, nil)
		}
		return variableRenameTarget{dir: filepath.Dir(filename), names: names}, found, nil
	case bundle.BundleFileName:
		return variableRenameTarget{}, false, nil
	}
	if filepath.Ext(filename) != ".yaml" && filepath.Ext(filename) != ".yml" {
		return variableRenameTarget{}, false, nil
	}
	offset, ok := byteAtPosition(doc.Text, pos)
	if !ok {
		return variableRenameTarget{}, false, nil
	}
	names := templateVariableAt(doc.Text, offset)
	if len(names) == 0 {
		return variableRenameTarget{}, false, nil
	}
	dirs, err := s.bundleDirectoriesForValuesFile(ctx, filename)
	if err != nil {
		return variableRenameTarget{}, true, err
	}
	if len(dirs) == 0 {
		return variableRenameTarget{}, false, nil
	}
	if len(dirs) > 1 {
		return variableRenameTarget{}, true, errors.New("values file is referenced by multiple bundles; rename from a variables definition instead")
	}
	return variableRenameTarget{dir: dirs[0], names: names, fromValues: true}, true, nil
}

func documentBaseName(doc document) string {
	filename, local := fileURIPath(doc.URI)
	if !local {
		return doc.URI
	}
	return filepath.Base(filename)
}

func hasVariableDefinition(doc document, names []string) bool {
	r, found := variableDefinitionRange(doc, names)
	return found && r.Start.Byte > 0
}

func hasLiteralVariableKey(doc document, names []string) bool {
	r, found := variableKeyRange(doc, names)
	return found && r.Start.Byte > 0
}

func variableKeyRange(doc document, names []string) (hcl.Range, bool) {
	if len(names) == 0 {
		return hcl.Range{}, false
	}
	file, _ := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
	if file == nil {
		return hcl.Range{}, false
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok || body.Attributes["variables"] == nil {
		return hcl.Range{}, false
	}
	return objectKeyRange(body.Attributes["variables"].Expr, names)
}

func objectKeyRange(expr hcl.Expression, names []string) (hcl.Range, bool) {
	items, diagnostics := hcl.ExprMap(expr)
	if diagnostics.HasErrors() {
		return hcl.Range{}, false
	}
	for _, item := range items {
		key, diagnostics := item.Key.Value(nil)
		if diagnostics.HasErrors() || !key.IsKnown() || key.IsNull() || key.Type() != cty.String || key.AsString() != names[0] {
			continue
		}
		if len(names) == 1 {
			return item.Key.Range(), true
		}
		return objectKeyRange(item.Value, names[1:])
	}
	return hcl.Range{}, false
}

func templateVariableRenameEdits(text string, names []string, newName string) []textEdit {
	leaf := names[len(names)-1]
	offsets := templateVariableReferenceOffsets(text, names)
	sort.Ints(offsets)
	seen := make(map[documentRange]struct{})
	var result []textEdit
	for _, offset := range offsets {
		r := templateVariableHoverRange(text, offset, leaf)
		start, ok := byteAtPosition(text, r.Start)
		if !ok {
			continue
		}
		end, ok := byteAtPosition(text, r.End)
		if !ok || start == end {
			continue
		}
		oldText := text[start:end]
		quoted := oldText == `""` || oldText == "``" || start > 0 && (text[start-1] == '"' || text[start-1] == '`')
		if !quoted && (strings.HasPrefix(oldText, "$") || oldText == ".") {
			continue
		}
		if _, duplicate := seen[r]; duplicate {
			continue
		}
		seen[r] = struct{}{}
		replacement := newName
		if oldText == `""` {
			replacement = strconv.Quote(newName)
		} else if oldText == "``" {
			replacement = "`" + newName + "`"
		} else if start > 0 && text[start-1] == '"' {
			quoted := strconv.Quote(newName)
			replacement = quoted[1 : len(quoted)-1]
		}
		result = append(result, textEdit{Range: r, NewText: replacement})
	}
	return result
}
