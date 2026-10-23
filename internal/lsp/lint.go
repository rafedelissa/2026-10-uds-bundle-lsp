// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
)

const (
	LintSeverityError       = "error"
	LintSeverityWarning     = "warning"
	LintSeverityInformation = "information"
	LintSeverityHint        = "hint"
)

// LintDiagnostic is a source diagnostic produced by Lint.
type LintDiagnostic struct {
	Path     string `json:"path" yaml:"path"`
	Line     int    `json:"line" yaml:"line"`
	Column   int    `json:"column" yaml:"column"`
	Severity string `json:"severity" yaml:"severity"`
	Message  string `json:"message" yaml:"message"`
}

// Lint scans a bundle workspace using the same diagnostics engine as the language server.
func Lint(ctx context.Context, root string) ([]LintDiagnostic, error) {
	root, store, err := indexBundleWorkspace(ctx, root, "lint")
	if err != nil {
		return nil, err
	}

	workspaceResults := store.workspaceDiagnostics(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]LintDiagnostic, 0)
	for uri, diagnostics := range workspaceResults {
		path := lintDiagnosticPath(root, uri)
		for _, item := range diagnostics {
			result = append(result, LintDiagnostic{
				Path:     path,
				Line:     item.Range.Start.Line + 1,
				Column:   item.Range.Start.Character + 1,
				Severity: lintSeverity(item.Severity),
				Message:  item.Message,
			})
		}
	}
	slices.SortFunc(result, func(a, b LintDiagnostic) int {
		if n := cmp.Compare(a.Path, b.Path); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Line, b.Line); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Column, b.Column); n != 0 {
			return n
		}
		return cmp.Compare(a.Message, b.Message)
	})
	return result, nil
}

func lintDiagnosticPath(root, uri string) string {
	filename, ok := fileURIPath(uri)
	if !ok {
		return uri
	}
	relative, err := filepath.Rel(root, filename)
	if err != nil {
		return filename
	}
	return filepath.ToSlash(relative)
}

func lintSeverity(severity int) string {
	switch severity {
	case 1:
		return LintSeverityError
	case 2:
		return LintSeverityWarning
	case 3:
		return LintSeverityInformation
	case 4:
		return LintSeverityHint
	default:
		return LintSeverityError
	}
}
