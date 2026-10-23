package lsp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Recompute indexed and open bundles. Keeping a complete publication per URI
// clears results for removed references and preserves all bundle contexts when
// multiple bundles share a values file.
func (s *documentStore) refreshDiagnostics(ctx context.Context, writer *bufio.Writer) error {
	results := s.workspaceDiagnostics(ctx)
	active := make(map[string]struct{}, len(results))
	for uri := range results {
		active[uri] = struct{}{}
	}
	for uri := range s.publishedDiagnostics {
		if _, found := results[uri]; !found {
			results[uri] = nil
		}
	}
	for _, uri := range slices.Sorted(maps.Keys(results)) {
		params := publishDiagnosticsParams{URI: uri, Diagnostics: results[uri]}
		if params.Diagnostics == nil {
			params.Diagnostics = make([]diagnostic, 0)
		}
		if doc, found := s.document(uri); found && s.diagnosticVersionSupport {
			params.Version = &doc.Version
		}
		if err := writeMessage(writer, struct {
			JSONRPC string                   `json:"jsonrpc"`
			Method  string                   `json:"method"`
			Params  publishDiagnosticsParams `json:"params"`
		}{JSONRPC: jsonRPCVersion, Method: "textDocument/publishDiagnostics", Params: params}); err != nil {
			return err
		}
	}
	s.publishedDiagnostics = active
	return nil
}

func (s *documentStore) workspaceDiagnostics(ctx context.Context) map[string][]diagnostic {
	results := make(map[string][]diagnostic)
	defer s.relativizeDiagnosticMessages(results)
	for _, filename := range slices.Sorted(maps.Keys(s.workspaceFiles)) {
		if ctx.Err() != nil {
			return results
		}
		if isVariablesFile(filename) {
			s.variablesAtPath(ctx, filename, results)
		} else if _, err := s.sourceDocument(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
			results[localFileURI(filename)] = []diagnostic{{Severity: 1, Source: "uds-lsp", Message: err.Error()}}
		}
	}
	for _, uri := range slices.Sorted(maps.Keys(s.documents)) {
		filename, ok := fileURIPath(uri)
		if ok && isVariablesFile(filename) {
			s.variablesAtPath(ctx, filename, results)
		}
	}
	for _, doc := range s.diagnosticBundles(ctx) {
		if ctx.Err() != nil {
			break
		}
		s.validateBundleAssets(ctx, doc, results)
	}
	return results
}

func (s *documentStore) relativizeDiagnosticMessages(results map[string][]diagnostic) {
	prefixes := make([]string, 0, len(s.workspaceFolders)*3)
	for root, folder := range s.workspaceFolders {
		root = filepath.Clean(root)
		prefixes = append(prefixes, root+string(filepath.Separator), filepath.ToSlash(root)+"/")
		if folder.URI != "" {
			prefixes = append(prefixes, strings.TrimSuffix(folder.URI, "/")+"/")
		}
	}
	slices.SortFunc(prefixes, func(a, b string) int { return len(b) - len(a) })
	for _, diagnostics := range results {
		for i := range diagnostics {
			for _, prefix := range prefixes {
				diagnostics[i].Message = strings.ReplaceAll(diagnostics[i].Message, prefix, "")
			}
		}
	}
}

func (s *documentStore) diagnosticBundles(ctx context.Context) []document {
	bundles := make(map[string]document)
	for _, filename := range slices.Sorted(maps.Keys(s.workspaceFiles)) {
		if filepath.Base(filename) != bundle.BundleFileName {
			continue
		}
		if doc, err := s.sourceDocument(filename); err == nil {
			bundles[doc.URI] = doc
		}
	}
	for _, uri := range slices.Sorted(maps.Keys(s.documents)) {
		doc := s.documents[uri]
		doc.URI = uri
		parsedURI, err := url.Parse(uri)
		if err != nil {
			continue
		}
		if path.Base(parsedURI.Path) == bundle.BundleFileName {
			bundles[uri] = doc
			continue
		}
		filename, ok := fileURIPath(uri)
		if !ok {
			continue
		}
		if !isVariablesFile(filename) && filepath.Ext(filename) != ".yaml" && filepath.Ext(filename) != ".yml" {
			continue
		}
		if len(s.workspaceFolders) > 0 && s.workspaceIncludes(filename) {
			continue // The workspace index already covers ancestor bundles.
		}
		for dir := filepath.Dir(filename); ; dir = filepath.Dir(dir) {
			candidate, err := s.sourceDocument(filepath.Join(dir, bundle.BundleFileName))
			if err == nil {
				candidateURI, parseErr := url.Parse(candidate.URI)
				if parseErr != nil {
					break
				}
				_, arch, _ := s.variablesAtPath(ctx, filepath.Join(dir, bundle.BundleConfigFileName), make(map[string][]diagnostic))
				parsed, _ := parseBundleDocument(ctx, candidate, candidateURI, arch)
				if isVariablesFile(filename) || (parsed != nil && referencesValuesFile(parsed, filepath.Join(dir, bundle.BundleFileName), filename)) {
					bundles[candidate.URI] = candidate
				}
			}
			if isVariablesFile(filename) || filepath.Dir(dir) == dir {
				break
			}
		}
	}
	result := make([]document, 0, len(bundles))
	for _, uri := range slices.Sorted(maps.Keys(bundles)) {
		result = append(result, bundles[uri])
	}
	return result
}

func (s *documentStore) validateBundleAssets(ctx context.Context, doc document, results map[string][]diagnostic) {
	uri, err := url.Parse(doc.URI)
	if err != nil {
		return
	}
	filename, local := fileURIPath(doc.URI)
	var (
		variables bundle.Variables
		arch      string
		related   []diagnosticRelatedInformation
		validVars = true
	)
	if local {
		dir := filepath.Dir(filename)
		defaults, _, defaultsOK := s.variablesAtPath(ctx, filepath.Join(dir, bundle.BundleDefaultsFileName), results)
		config, configArch, configOK := s.variablesAtPath(ctx, filepath.Join(dir, bundle.BundleConfigFileName), results)
		variables = bundle.MergeVariables(defaults, config)
		arch = configArch
		validVars = defaultsOK && configOK
		for _, name := range []string{bundle.BundleDefaultsFileName, bundle.BundleConfigFileName} {
			varsDoc, err := s.sourceDocument(filepath.Join(dir, name))
			if err == nil {
				related = append(related, diagnosticRelatedInformation{
					Location: location{URI: varsDoc.URI, Range: variablesRange(varsDoc)},
					Message:  "variables evaluated from " + name,
				})
			}
		}
	}
	parsed, diagnostics := parseBundleDocument(ctx, doc, uri, arch)
	results[doc.URI] = diagnostics
	if parsed == nil || !local {
		return
	}
	for _, pkg := range parsed.Packages {
		for i, valuesFile := range pkg.ValuesFiles {
			if ctx.Err() != nil {
				return
			}
			resolved := valuesFile
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(filename), resolved)
			}
			r := valuesFileRange(doc, pkg.Name, i)
			valuesDoc, err := s.sourceDocument(resolved)
			if err != nil {
				results[doc.URI] = append(results[doc.URI], diagnostic{
					Range: r, Severity: 1, Source: "uds-lsp",
					Message: fmt.Sprintf("package %q values file %q: %v", pkg.Name, valuesFile, err),
				})
				continue
			}
			if _, found := results[valuesDoc.URI]; !found {
				results[valuesDoc.URI] = nil
			}
			refs := []diagnosticRelatedInformation{{
				Location: location{URI: doc.URI, Range: r},
				Message:  fmt.Sprintf("values file referenced by package %q", pkg.Name),
			}}
			refs = append(refs, related...)
			if d := valuesTemplateDiagnostic(valuesDoc, variables, validVars); d != nil {
				d.Message = fmt.Sprintf("package %q in %s (adjacent defaults/config): %s", pkg.Name, doc.URI, d.Message)
				d.RelatedInformation = refs
				results[valuesDoc.URI] = append(results[valuesDoc.URI], *d)
			}
		}
	}
}

func (s *documentStore) variablesAtPath(ctx context.Context, filename string, results map[string][]diagnostic) (bundle.Variables, string, bool) {
	doc, err := s.sourceDocument(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", true // Both adjacent files are optional.
	}
	if err != nil {
		results[localFileURI(filename)] = []diagnostic{{Severity: 1, Source: "uds-lsp", Message: err.Error()}}
		return nil, "", false
	}
	var (
		variables bundle.Variables
		arch      string
	)
	if filepath.Base(filename) == bundle.BundleDefaultsFileName {
		variables, err = bundle.ParseDefaultsSource(ctx, []byte(doc.Text), filename)
	} else {
		var config *bundle.UDSBundleConfig
		config, err = bundle.NewHCLParser("", iostreams.IOStreams{}).ParseBundleConfigSource(ctx, []byte(doc.Text), filename)
		if err == nil {
			variables = config.Variables
			if config.Options != nil {
				arch = config.Options.Architecture
			}
		}
	}
	results[doc.URI] = nil
	if err != nil {
		diagnostics := diagnosticsForError(err, doc.Text)
		for i := range diagnostics {
			if diagnostics[i].Range == (documentRange{}) {
				diagnostics[i].Range = variablesRange(doc)
			}
		}
		results[doc.URI] = diagnostics
	}
	return variables, arch, err == nil
}

func isVariablesFile(filename string) bool {
	return filepath.Base(filename) == bundle.BundleDefaultsFileName || filepath.Base(filename) == bundle.BundleConfigFileName
}

func variablesRange(doc document) documentRange {
	file, _ := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
	if file != nil {
		if body, ok := file.Body.(*hclsyntax.Body); ok {
			if attr := body.Attributes["variables"]; attr != nil {
				return rangeFromHCL(doc.Text, attr.Expr.Range())
			}
		}
	}
	return documentRange{}
}

func valuesFileRange(doc document, packageName string, index int) documentRange {
	file, _ := hclsyntax.ParseConfig([]byte(doc.Text), doc.URI, hcl.InitialPos)
	if file == nil {
		return documentRange{}
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return documentRange{}
	}
	for _, block := range body.Blocks {
		if block.Type != "package" || len(block.Labels) != 1 || block.Labels[0] != packageName {
			continue
		}
		attr := block.Body.Attributes["values_files"]
		if attr == nil {
			continue
		}
		expressions, diags := hcl.ExprList(attr.Expr)
		if !diags.HasErrors() && index < len(expressions) {
			return rangeFromHCL(doc.Text, expressions[index].Range())
		}
		return rangeFromHCL(doc.Text, attr.Expr.Range())
	}
	return documentRange{}
}

func (s *documentStore) sourceDocument(filename string) (document, error) {
	filename = filepath.Clean(filename)
	for _, uri := range slices.Sorted(maps.Keys(s.documents)) {
		if docPath, ok := fileURIPath(uri); ok && docPath == filename {
			doc := s.documents[uri]
			doc.URI = uri
			return doc, nil
		}
	}
	doc := document{URI: localFileURI(filename)}
	info, err := os.Stat(filename)
	if err != nil {
		return doc, err
	}
	if !info.Mode().IsRegular() {
		return doc, fmt.Errorf("%q is not a regular file", filename)
	}
	src, err := os.ReadFile(filename)
	doc.Text = string(src)
	return doc, err
}

func localFileURI(filename string) string {
	filename = filepath.ToSlash(filename)
	if !filepath.IsAbs(filename) || filename[0] != '/' {
		filename = "/" + filename
	}
	return (&url.URL{Scheme: "file", Path: filename}).String()
}
