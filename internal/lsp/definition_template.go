package lsp

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
)

type templateDefinitionScope struct {
	dot       []string
	variables map[string][]string
}

// templateVariableAt resolves statically traceable object paths. Parsing the
// template keeps text, comments, quoted strings, and changed dot scopes from
// being mistaken for root variable references.
func templateVariableAt(text string, offset int) []string {
	tmpl, err := template.New("values").Parse(text)
	if err != nil {
		return nil
	}
	return templateVariableAtTree(tmpl, offset)
}

func templateVariableAtTree(tmpl *template.Template, offset int) []string {
	scope := templateDefinitionScope{dot: []string{}, variables: map[string][]string{"$": {}}}
	names := templateListReference(tmpl.Root, offset, scope)
	if len(names) < 2 || names[0] != "vars" {
		return nil
	}
	return names[1:]
}

func templateReferencesVariable(text string, names []string) bool {
	if len(names) == 0 {
		return false
	}
	tmpl, err := template.New("values").Parse(text)
	if err != nil {
		return false
	}
	leaf := names[len(names)-1]
	if leaf == "" {
		for offset := range len(text) {
			if slices.Equal(templateVariableAtTree(tmpl, offset), names) {
				return true
			}
		}
		return false
	}
	needles := []string{leaf}
	if quoted := strconv.Quote(leaf); quoted[1:len(quoted)-1] != leaf {
		needles = append(needles, quoted[1:len(quoted)-1])
	}
	for _, needle := range needles {
		for start := 0; start < len(text); {
			index := strings.Index(text[start:], needle)
			if index < 0 {
				break
			}
			offset := start + index
			if slices.Equal(templateVariableAtTree(tmpl, offset), names) {
				return true
			}
			start = offset + len(needle)
		}
	}
	return false
}

func templateListReference(list *parse.ListNode, offset int, scope templateDefinitionScope) []string {
	if list == nil {
		return nil
	}
	for _, node := range list.Nodes {
		switch node := node.(type) {
		case *parse.ActionNode:
			if names := templateNodeReference(node.Pipe, offset, scope); names != nil {
				return names
			}
			bindTemplateVariables(node.Pipe, scope, templateNodePath(node.Pipe, scope))
		case *parse.IfNode:
			if names := templateBranchReference(&node.BranchNode, offset, scope); names != nil {
				return names
			}
			invalidateTemplateAssignments(&node.BranchNode, scope)
		case *parse.WithNode:
			if names := templateBranchReference(&node.BranchNode, offset, scope); names != nil {
				return names
			}
			invalidateTemplateAssignments(&node.BranchNode, scope)
		case *parse.RangeNode:
			if names := templateBranchReference(&node.BranchNode, offset, scope); names != nil {
				return names
			}
			invalidateTemplateAssignments(&node.BranchNode, scope)
		case *parse.TemplateNode:
			if names := templateNodeReference(node.Pipe, offset, scope); names != nil {
				return names
			}
		}
	}
	return nil
}

// An assignment in a conditional or loop can change an outer alias at runtime.
// Forget that alias rather than navigate subsequent uses to a stale object.
func invalidateTemplateAssignments(branch *parse.BranchNode, scope templateDefinitionScope) {
	if branch.Pipe.IsAssign {
		bindTemplateVariables(branch.Pipe, scope, nil)
	}
	for _, list := range []*parse.ListNode{branch.List, branch.ElseList} {
		if list == nil {
			continue
		}
		for _, node := range list.Nodes {
			switch node := node.(type) {
			case *parse.ActionNode:
				if node.Pipe.IsAssign {
					bindTemplateVariables(node.Pipe, scope, nil)
				}
			case *parse.IfNode:
				invalidateTemplateAssignments(&node.BranchNode, scope)
			case *parse.WithNode:
				invalidateTemplateAssignments(&node.BranchNode, scope)
			case *parse.RangeNode:
				invalidateTemplateAssignments(&node.BranchNode, scope)
			}
		}
	}
}

func templateBranchReference(node *parse.BranchNode, offset int, scope templateDefinitionScope) []string {
	if names := templateNodeReference(node.Pipe, offset, scope); names != nil {
		return names
	}
	value := templateNodePath(node.Pipe, scope)
	inner := templateDefinitionScope{dot: scope.dot, variables: maps.Clone(scope.variables)}
	if node.NodeType == parse.NodeRange {
		// A range element has no single static object path. Preserve $ for
		// explicit root references, but do not guess element/iterator targets.
		value = nil
	}
	bindTemplateVariables(node.Pipe, inner, value)
	otherwise := templateDefinitionScope{dot: scope.dot, variables: maps.Clone(inner.variables)}
	if node.NodeType == parse.NodeWith || node.NodeType == parse.NodeRange {
		inner.dot = value
	}
	if names := templateListReference(node.List, offset, inner); names != nil {
		return names
	}
	return templateListReference(node.ElseList, offset, otherwise)
}

func bindTemplateVariables(pipe *parse.PipeNode, scope templateDefinitionScope, value []string) {
	if pipe == nil {
		return
	}
	for _, decl := range pipe.Decl {
		scope.variables[decl.Ident[0]] = value
	}
}

func templateNodeReference(node parse.Node, offset int, scope templateDefinitionScope) []string {
	switch node := node.(type) {
	case *parse.DotNode:
		start := int(node.Pos)
		if scope.dot != nil && offset >= start && offset <= start+1 {
			return slices.Clone(scope.dot)
		}
	case *parse.FieldNode:
		start := int(node.Pos)
		// The template parser folds chained fields into one node, retaining
		// the position of the second field rather than the first dot.
		if len(node.Ident) > 1 {
			start -= len(node.Ident[0]) + 1
		}
		return templateFieldReference(scope.dot, node.Ident, start, offset)
	case *parse.VariableNode:
		base := scope.variables[node.Ident[0]]
		start := int(node.Pos)
		if len(node.Ident) > 1 {
			start -= len(node.Ident[0])
		}
		if base != nil && offset >= start && offset <= start+len(node.Ident[0]) {
			return base
		}
		return templateFieldReference(base, node.Ident[1:], start+len(node.Ident[0]), offset)
	case *parse.ChainNode:
		if names := templateNodeReference(node.Node, offset, scope); names != nil {
			return names
		}
		return templateFieldReference(templateNodePath(node.Node, scope), node.Field, int(node.Pos), offset)
	case *parse.PipeNode:
		if node == nil {
			return nil
		}
		for _, cmd := range node.Cmds {
			if names := templateNodeReference(cmd, offset, scope); names != nil {
				return names
			}
		}
	case *parse.CommandNode:
		for _, arg := range node.Args {
			if names := templateNodeReference(arg, offset, scope); names != nil {
				return names
			}
		}
		if !isTemplateIndex(node) {
			return nil
		}
		path := templateNodePath(node.Args[1], scope)
		for _, arg := range node.Args[2:] {
			key, ok := arg.(*parse.StringNode)
			if !ok || path == nil {
				return nil
			}
			path = append(slices.Clone(path), key.Text)
			if offset >= int(key.Pos) && offset <= int(key.Pos)+len(key.Quoted) {
				return path
			}
		}
	}
	return nil
}

func templateFieldReference(base, fields []string, start, offset int) []string {
	if base == nil {
		return nil
	}
	path := slices.Clone(base)
	for _, field := range fields {
		start++ // Skip the separating dot.
		path = append(path, field)
		if offset >= start && offset <= start+len(field) {
			return path
		}
		start += len(field)
	}
	return nil
}

func templateNodePath(node parse.Node, scope templateDefinitionScope) []string {
	switch node := node.(type) {
	case *parse.DotNode:
		return scope.dot
	case *parse.FieldNode:
		if scope.dot != nil {
			return append(slices.Clone(scope.dot), node.Ident...)
		}
	case *parse.VariableNode:
		if base := scope.variables[node.Ident[0]]; base != nil {
			return append(slices.Clone(base), node.Ident[1:]...)
		}
	case *parse.ChainNode:
		if base := templateNodePath(node.Node, scope); base != nil {
			return append(slices.Clone(base), node.Field...)
		}
	case *parse.PipeNode:
		if node != nil && len(node.Cmds) == 1 {
			return templateNodePath(node.Cmds[0], scope)
		}
	case *parse.CommandNode:
		if len(node.Args) == 1 {
			return templateNodePath(node.Args[0], scope)
		}
		if !isTemplateIndex(node) {
			return nil
		}
		path := templateNodePath(node.Args[1], scope)
		for _, arg := range node.Args[2:] {
			key, ok := arg.(*parse.StringNode)
			if !ok || path == nil {
				return nil
			}
			path = append(slices.Clone(path), key.Text)
		}
		return path
	}
	return nil
}

func isTemplateIndex(node *parse.CommandNode) bool {
	if len(node.Args) < 3 {
		return false
	}
	name, ok := node.Args[0].(*parse.IdentifierNode)
	return ok && name.Ident == "index"
}
