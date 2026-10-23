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

	iostreams.IOStreams
}

// NewFmtOptions returns a new FmtOptions with default values.
func NewFmtOptions(streams iostreams.IOStreams) *FmtOptions {
	return &FmtOptions{IOStreams: streams}
}

// NewFmtCommand creates the fmt command.
func NewFmtCommand(streams iostreams.IOStreams) *cobra.Command {
	o := NewFmtOptions(streams)
	return &cobra.Command{
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
	for _, doc := range documents {
		if err := verifyFormatSource(doc.Path, doc.OriginalText); err != nil {
			return err
		}
	}
	result := fmtResult{Formatted: make([]string, 0, len(documents))}
	for _, doc := range documents {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeFormattedDocument(doc); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, doc.Path)
		if err != nil {
			relative = doc.Path
		}
		result.Formatted = append(result.Formatted, filepath.ToSlash(relative))
	}
	if err := o.printResult(result); err != nil {
		return fmt.Errorf("print format result: %w", err)
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

func (o *FmtOptions) printResult(result fmtResult) error {
	if o.Format != printer.FormatText {
		p, err := printer.NewPrinter(o.Format)
		if err != nil {
			return err
		}
		return p.PrintObj(result, o.Out())
	}
	if len(result.Formatted) == 0 {
		return writeSourceCommandOutput(o.Out(), "No formatting changes required.\n")
	}
	for _, path := range result.Formatted {
		if err := writeSourceCommandOutput(o.Out(), "Formatted "+path+"\n"); err != nil {
			return err
		}
	}
	return nil
}
