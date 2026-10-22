package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/defenseunicorns/uds-cli/pkg/bundle/spec"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/hashicorp/hcl/v2"
)

type diagnostic struct {
	Range    documentRange `json:"range"`
	Severity int           `json:"severity"`
	Source   string        `json:"source"`
	Message  string        `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int32       `json:"version,omitempty"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

func (s *documentStore) publishDiagnostics(ctx context.Context, writer *bufio.Writer, params json.RawMessage) error {
	// All synchronization notifications identify their document in this field.
	var parsed struct {
		TextDocument textDocumentIdentifier `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &parsed); err != nil {
		return err
	}
	uri, err := url.Parse(parsed.TextDocument.URI)
	if err != nil {
		return fmt.Errorf("parse document URI: %w", err)
	}
	if path.Base(uri.Path) != bundle.BundleFileName {
		return nil
	}
	result := publishDiagnosticsParams{
		URI:         parsed.TextDocument.URI,
		Diagnostics: make([]diagnostic, 0),
	}
	if doc, found := s.document(result.URI); found {
		result.Diagnostics = bundleDiagnostics(ctx, doc, uri)
		if s.diagnosticVersionSupport {
			result.Version = &doc.Version
		}
	}
	// A closed document publishes an empty array to clear its previous results.
	return writeMessage(writer, struct {
		JSONRPC string                   `json:"jsonrpc"`
		Method  string                   `json:"method"`
		Params  publishDiagnosticsParams `json:"params"`
	}{JSONRPC: jsonRPCVersion, Method: "textDocument/publishDiagnostics", Params: result})
}

func bundleDiagnostics(ctx context.Context, doc document, uri *url.URL) []diagnostic {
	streams := iostreams.IOStreams{}
	parser := bundle.NewHCLParser("", streams)
	// Only local file URIs supply a directory for file(). Never read the bundle
	// itself from disk: the synchronized buffer is the source of truth.
	filename := filepath.FromSlash(uri.Path)
	if runtime.GOOS == "windows" && strings.HasPrefix(filename, `\`) {
		filename = strings.TrimPrefix(filename, `\`)
	}
	var (
		b   *spec.UDSBundle
		err error
	)
	if uri.Scheme == "file" && (uri.Host == "" || uri.Host == "localhost") && filepath.IsAbs(filename) {
		b, err = parser.ParseBundleSource(ctx, []byte(doc.Text), filename)
	} else {
		b, err = parser.ParseBundleBytes(ctx, []byte(doc.Text))
	}
	if err == nil {
		err = b.Validate()
		if err == nil {
			_, err = bundle.BuildDependencyGraph(ctx, streams, b)
		}
	}
	if err == nil {
		return make([]diagnostic, 0)
	}
	return diagnosticsForError(err, doc.Text)
}

func diagnosticsForError(err error, text string) []diagnostic {
	var hclDiags hcl.Diagnostics
	if errors.As(err, &hclDiags) {
		result := make([]diagnostic, 0, len(hclDiags))
		for _, d := range hclDiags {
			item := diagnostic{Source: "uds-lsp", Severity: 1, Message: d.Summary}
			if d.Detail != "" {
				item.Message += ": " + d.Detail
			}
			if d.Severity == hcl.DiagWarning {
				item.Severity = 2
			}
			if d.Subject != nil {
				item.Range = rangeFromHCL(text, *d.Subject)
			}
			result = append(result, item)
		}
		return result
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var result []diagnostic
		for _, child := range joined.Unwrap() {
			result = append(result, diagnosticsForError(child, text)...)
		}
		return result
	}
	item := diagnostic{Source: "uds-lsp", Severity: 1, Message: err.Error()}
	var duplicate *bundle.DuplicateLocalError
	var undefined *bundle.UndefinedLocalDependencyError
	switch {
	case errors.As(err, &duplicate):
		item.Range = rangeFromHCL(text, duplicate.Duplicate)
	case errors.As(err, &undefined):
		item.Range = rangeFromHCL(text, undefined.Position)
	}
	return []diagnostic{item}
}

func rangeFromHCL(text string, r hcl.Range) documentRange {
	return documentRange{
		Start: positionAtByte(text, r.Start.Byte),
		End:   positionAtByte(text, r.End.Byte),
	}
}

// HCL ranges use byte offsets; LSP positions use zero-based UTF-16 code units.
func positionAtByte(text string, offset int) position {
	offset = max(0, min(offset, len(text)))
	prefix := text[:offset]
	lineStart := strings.LastIndexByte(prefix, '\n') + 1
	result := position{Line: strings.Count(prefix, "\n")}
	for _, r := range prefix[lineStart:] {
		result.Character++
		if r > 0xffff {
			result.Character++
		}
	}
	return result
}
