// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/defenseunicorns/uds-cli/internal/lsp"
	"github.com/defenseunicorns/uds-cli/internal/printer"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validLintBundle = `uds {
  bundle_api_version = "uds.dev/v1alpha1"
}
metadata {
  name = "test"
  version = "1.0.0"
}
package "app" {
  source = "oci://example.com/app:1.0.0"
  signature_verification {
    verify = false
  }
}
`

func TestLintCommandReportsDiagnostics(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, bundleFileName), []byte(`package "app" {`), 0o600))
	streams, _, out, _ := iostreams.NewTestIOStreams()
	cmd := NewLintCommand(streams)
	cmd.SetArgs([]string{root})

	err := cmd.ExecuteContext(t.Context())

	require.Error(t, err)
	require.ErrorIs(t, err, ErrLintFailed)
	assert.Contains(t, out.String(), "bundle.uds.hcl:1:")
	assert.Contains(t, out.String(), ": error:")
}

func TestLintCommandSucceedsWithoutDiagnostics(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, bundleFileName), []byte(validLintBundle), 0o600))
	streams, _, out, _ := iostreams.NewTestIOStreams()
	cmd := NewLintCommand(streams)
	cmd.SetArgs([]string{root})

	require.NoError(t, cmd.ExecuteContext(t.Context()))
	assert.Equal(t, "No issues found.\n", out.String())
}

func TestLintCommandRejectsExplicitEmptyPath(t *testing.T) {
	cmd := NewLintCommand(iostreams.IOStreams{})
	cmd.SetArgs([]string{""})

	err := cmd.ExecuteContext(t.Context())

	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestLintCommandRejectsUnsupportedBundleFlags(t *testing.T) {
	for _, args := range [][]string{
		{"lint", "--architecture", "amd64"},
		{"lint", "--concurrency", "1"},
		{"lint", "--config", "custom.uds.hcl"},
		{"lint", "--plain-http"},
		{"lint", "--skip-tls-verify"},
		{"lint", "--tmp-dir", t.TempDir()},
	} {
		cmd := NewBundleCommand(iostreams.IOStreams{})
		cmd.SetArgs(args)

		err := cmd.ExecuteContext(t.Context())

		require.ErrorIs(t, err, ErrInvalidArgument)
		assert.Contains(t, err.Error(), "is not supported by bundle lint")
	}
}

func TestLintCommandDefaultsToCurrentDirectory(t *testing.T) {
	o := NewLintOptions(iostreams.IOStreams{})

	require.NoError(t, o.Complete(NewLintCommand(iostreams.IOStreams{}), nil))
	assert.Equal(t, ".", o.Path)
}

func TestLintOptionsPrintsStructuredResult(t *testing.T) {
	streams, _, out, _ := iostreams.NewTestIOStreams()
	o := LintOptions{IOStreams: streams, Format: printer.FormatJSON}
	result := lintResult{Diagnostics: []lsp.LintDiagnostic{{Path: "bundle.uds.hcl", Severity: lsp.LintSeverityError}}, Errors: 1}

	require.NoError(t, o.printResult(result))
	assert.JSONEq(t, `{"diagnostics":[{"path":"bundle.uds.hcl","line":0,"column":0,"severity":"error","message":""}],"errors":1,"warnings":0}`, out.String())
}

func TestNewLintResultCountsErrorsAndWarnings(t *testing.T) {
	result := newLintResult([]lsp.LintDiagnostic{
		{Severity: lsp.LintSeverityError},
		{Severity: lsp.LintSeverityWarning},
		{Severity: lsp.LintSeverityInformation},
	})

	assert.Equal(t, 1, result.Errors)
	assert.Equal(t, 1, result.Warnings)
}
