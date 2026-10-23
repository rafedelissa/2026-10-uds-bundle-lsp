// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package bundle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/lsp"
	"github.com/defenseunicorns/uds-cli/internal/printer"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/spf13/cobra"
)

// FmtOptions holds the options for the fmt command.
type FmtOptions struct {
	Path   string
	Format printer.Format
	Check  bool

	iostreams.IOStreams
}

// NewFmtOptions returns a new FmtOptions with default values.
func NewFmtOptions(streams iostreams.IOStreams) *FmtOptions {
	return &FmtOptions{IOStreams: streams}
}

// NewFmtCommand creates the fmt command.
func NewFmtCommand(streams iostreams.IOStreams) *cobra.Command {
	o := NewFmtOptions(streams)
	cmd := &cobra.Command{
		Use:   "fmt [directory]",
		Short: "Format UDS bundle source files",
		Long:  "Format UDS bundle HCL files using the same canonical formatter as the UDS language server.",
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
	cmd.Flags().BoolVar(&o.Check, "check", false, "Check formatting without writing changes")
	return cmd
}

// Complete fills in options from command-line arguments.
func (o *FmtOptions) Complete(cmd *cobra.Command, args []string) error {
	path, format, err := completeSourceOptions(cmd, args, "fmt")
	if err != nil {
		return err
	}
	o.Path = path
	o.Format = format
	return nil
}

// Validate validates the fmt options without modifying state.
func (o *FmtOptions) Validate() error {
	if strings.TrimSpace(o.Path) == "" {
		return fmt.Errorf("format path must not be empty: %w", ErrInvalidArgument)
	}
	return nil
}

// Run executes the fmt command.
func (o *FmtOptions) Run(ctx context.Context) error {
	documents, err := lsp.Format(ctx, o.Path)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(o.Path)
	if err != nil {
		return fmt.Errorf("resolve format path %q: %w", o.Path, err)
	}
	paths := make([]string, 0, len(documents))
	for _, doc := range documents {
		relative, err := filepath.Rel(root, doc.Path)
		if err != nil {
			relative = doc.Path
		}
		paths = append(paths, filepath.ToSlash(relative))
	}
	if !o.Check {
		for _, doc := range documents {
			if err := verifyFormatSource(doc.Path, doc.OriginalText); err != nil {
				return err
			}
		}
		for _, doc := range documents {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := writeFormattedDocument(doc); err != nil {
				return err
			}
		}
	}
	if err := o.printResult(paths); err != nil {
		return fmt.Errorf("print format result: %w", err)
	}
	if o.Check && len(documents) > 0 {
		file := "files"
		if len(documents) == 1 {
			file = "file"
		}
		return fmt.Errorf("format check found %d unformatted %s: %w", len(documents), file, ErrFormatCheckFailed)
	}
	return nil
}

func verifyFormatSource(path, expected string) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve formatting target %q: %w", path, err)
	}
	return verifyResolvedFormatSource(path, target, expected)
}

func verifyResolvedFormatSource(path, target, expected string) error {
	current, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("read formatting target %q: %w", path, err)
	}
	if string(current) != expected {
		return fmt.Errorf("formatting target %q: %w", path, ErrFormatConflict)
	}
	return nil
}

func writeFormattedDocument(doc lsp.FormattedDocument) (retErr error) {
	target, err := filepath.EvalSymlinks(doc.Path)
	if err != nil {
		return fmt.Errorf("resolve formatting target %q: %w", doc.Path, err)
	}
	if err := verifyResolvedFormatSource(doc.Path, target, doc.OriginalText); err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("access %q for formatting: %w", doc.Path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("formatting target %q is not a regular file: %w", doc.Path, ErrInvalidPath)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".uds-fmt-*")
	if err != nil {
		return fmt.Errorf("create temporary formatting file for %q: %w", doc.Path, err)
	}
	closed := false
	renamed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, temp.Close())
		}
		if !renamed {
			if cleanupErr := os.Remove(temp.Name()); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove temporary formatting file: %w", cleanupErr))
			}
		}
	}()
	written, err := io.WriteString(temp, doc.Text)
	if err != nil {
		return fmt.Errorf("write temporary formatting file for %q: %w", doc.Path, err)
	}
	if written != len(doc.Text) {
		return fmt.Errorf("write temporary formatting file for %q: %w", doc.Path, io.ErrShortWrite)
	}
	if err := temp.Chmod(info.Mode()); err != nil {
		return fmt.Errorf("set temporary formatting file permissions for %q: %w", doc.Path, err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary formatting file for %q: %w", doc.Path, err)
	}
	if err := temp.Close(); err != nil {
		closed = true
		return fmt.Errorf("close temporary formatting file for %q: %w", doc.Path, err)
	}
	closed = true
	if err := verifyResolvedFormatSource(doc.Path, target, doc.OriginalText); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), target); err != nil {
		return fmt.Errorf("replace formatted document %q: %w", doc.Path, err)
	}
	renamed = true
	return nil
}

type fmtResult struct {
	Formatted []string `json:"formatted" yaml:"formatted"`
}

type fmtCheckResult struct {
	Unformatted []string `json:"unformatted" yaml:"unformatted"`
}

func (o *FmtOptions) printResult(paths []string) error {
	if o.Format != printer.FormatText {
		p, err := printer.NewPrinter(o.Format)
		if err != nil {
			return err
		}
		if o.Check {
			return p.PrintObj(fmtCheckResult{Unformatted: paths}, o.Out())
		}
		return p.PrintObj(fmtResult{Formatted: paths}, o.Out())
	}
	if len(paths) == 0 {
		return writeSourceCommandOutput(o.Out(), "No formatting changes required.\n")
	}
	verb := "Formatted "
	if o.Check {
		verb = "Would format "
	}
	for _, path := range paths {
		if err := writeSourceCommandOutput(o.Out(), verb+path+"\n"); err != nil {
			return err
		}
	}
	return nil
}
