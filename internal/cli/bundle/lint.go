// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package bundle

import (
	"context"
	"fmt"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/lsp"
	"github.com/defenseunicorns/uds-cli/internal/printer"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/spf13/cobra"
)

// LintOptions holds the options for the lint command.
type LintOptions struct {
	Path   string
	Format printer.Format

	iostreams.IOStreams
}

// NewLintOptions returns a new LintOptions with default values.
func NewLintOptions(streams iostreams.IOStreams) *LintOptions {
	return &LintOptions{IOStreams: streams}
}

// NewLintCommand creates the lint command.
func NewLintCommand(streams iostreams.IOStreams) *cobra.Command {
	o := NewLintOptions(streams)
	cmd := &cobra.Command{
		Use:   "lint [directory]",
		Short: "Lint UDS bundle source files",
		Long:  "Lint UDS bundle source files and referenced values files using the UDS language server diagnostics.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.Complete(cmd, args); err != nil {
				return err
			}
			if err := o.Validate(); err != nil {
				return err
			}
			return o.Run(cmd.Context())
		},
	}
	return cmd
}

// Complete fills in options from command-line arguments.
func (o *LintOptions) Complete(cmd *cobra.Command, args []string) error {
	path, format, err := completeSourceOptions(cmd, args, "lint")
	if err != nil {
		return err
	}
	o.Path = path
	o.Format = format
	return nil
}

// Validate validates the lint options without modifying state.
func (o *LintOptions) Validate() error {
	if strings.TrimSpace(o.Path) == "" {
		return fmt.Errorf("lint path must not be empty: %w", ErrInvalidArgument)
	}
	return nil
}

// Run executes the lint command.
func (o *LintOptions) Run(ctx context.Context) error {
	diagnostics, err := lsp.Lint(ctx, o.Path)
	if err != nil {
		return err
	}
	result := newLintResult(diagnostics)
	if err := o.printResult(result); err != nil {
		return fmt.Errorf("print lint result: %w", err)
	}
	if result.Errors > 0 {
		diagnostic := "diagnostics"
		if result.Errors == 1 {
			diagnostic = "diagnostic"
		}
		return fmt.Errorf("lint found %d error %s: %w", result.Errors, diagnostic, ErrLintFailed)
	}
	return nil
}

type lintResult struct {
	Diagnostics []lsp.LintDiagnostic `json:"diagnostics" yaml:"diagnostics"`
	Errors      int                  `json:"errors" yaml:"errors"`
	Warnings    int                  `json:"warnings" yaml:"warnings"`
}

func newLintResult(diagnostics []lsp.LintDiagnostic) lintResult {
	result := lintResult{Diagnostics: diagnostics}
	for _, item := range diagnostics {
		switch item.Severity {
		case lsp.LintSeverityError:
			result.Errors++
		case lsp.LintSeverityWarning:
			result.Warnings++
		}
	}
	return result
}

func (o *LintOptions) printResult(result lintResult) error {
	if o.Format != printer.FormatText {
		p, err := printer.NewPrinter(o.Format)
		if err != nil {
			return err
		}
		return p.PrintObj(result, o.Out())
	}
	if len(result.Diagnostics) == 0 {
		return writeSourceCommandOutput(o.Out(), "No issues found.\n")
	}
	for _, item := range result.Diagnostics {
		line := fmt.Sprintf("%s:%d:%d: %s: %s\n", item.Path, item.Line, item.Column, item.Severity, item.Message)
		if err := writeSourceCommandOutput(o.Out(), line); err != nil {
			return err
		}
	}
	return nil
}
