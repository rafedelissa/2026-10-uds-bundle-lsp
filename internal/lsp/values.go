package lsp

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/defenseunicorns/uds-cli/pkg/bundle/spec"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

func (s *documentStore) variablesForValuesFile(ctx context.Context, uri string) bundle.Variables {
	valuesPath, ok := fileURIPath(uri)
	if !ok || (filepath.Ext(valuesPath) != ".yaml" && filepath.Ext(valuesPath) != ".yml") {
		return nil
	}

	bundlePath, ok := s.referencingBundle(ctx, valuesPath)
	if !ok {
		return nil
	}
	bundleDir := filepath.Dir(bundlePath)
	defaults := s.variableShapeAtPath(filepath.Join(bundleDir, bundle.BundleDefaultsFileName))
	config := s.variableShapeAtPath(filepath.Join(bundleDir, bundle.BundleConfigFileName))
	return bundle.MergeVariables(defaults, config)
}

func (s *documentStore) referencingBundle(ctx context.Context, valuesPath string) (string, bool) {
	parser := bundle.NewHCLParser("", iostreams.IOStreams{})
	valuesPath = filepath.Clean(valuesPath)
	openBundles := make(map[string]struct{})
	for _, doc := range s.documents {
		bundlePath, ok := fileURIPath(doc.URI)
		if !ok || filepath.Base(bundlePath) != bundle.BundleFileName {
			continue
		}
		bundlePath = filepath.Clean(bundlePath)
		openBundles[bundlePath] = struct{}{}
		parsed, err := parser.ParseBundleSource(ctx, []byte(doc.Text), bundlePath)
		if err == nil && referencesValuesFile(parsed, bundlePath, valuesPath) {
			return bundlePath, true
		}
	}

	for dir := filepath.Dir(valuesPath); ; dir = filepath.Dir(dir) {
		bundlePath := filepath.Join(dir, bundle.BundleFileName)
		if _, open := openBundles[bundlePath]; !open {
			parsed, err := parser.ParseBundleFile(ctx, bundlePath)
			if err == nil && referencesValuesFile(parsed, bundlePath, valuesPath) {
				return bundlePath, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
	}
}

func (s *documentStore) bundleDirectoriesForValuesFile(ctx context.Context, filename string) ([]string, error) {
	var result []string
	for _, candidate := range s.diagnosticBundles(ctx) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		bundlePath, ok := fileURIPath(candidate.URI)
		if !ok {
			continue
		}
		parsed, err := s.parseBundleForValues(ctx, candidate, bundlePath)
		if err != nil {
			return nil, err
		}
		if referencesValuesFile(parsed, bundlePath, filename) {
			dir := filepath.Dir(bundlePath)
			if !slices.Contains(result, dir) {
				result = append(result, dir)
			}
		}
	}
	slices.Sort(result)
	return result, nil
}

func (s *documentStore) valuesDocumentsForBundle(ctx context.Context, dir string) ([]document, error) {
	bundlePath := filepath.Join(dir, bundle.BundleFileName)
	candidate, err := s.sourceDocument(bundlePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read bundle %q for rename: %w", bundlePath, err)
	}
	parsed, err := s.parseBundleForValues(ctx, candidate, bundlePath)
	if err != nil {
		return nil, err
	}
	var result []document
	for _, pkg := range parsed.Packages {
		for _, name := range pkg.ValuesFiles {
			if !filepath.IsAbs(name) {
				name = filepath.Join(dir, name)
			}
			values, err := s.sourceDocument(name)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read values file %q for rename: %w", name, err)
			}
			if slices.ContainsFunc(result, func(existing document) bool { return existing.URI == values.URI }) {
				continue
			}
			result = append(result, values)
		}
	}
	slices.SortFunc(result, func(a, b document) int { return strings.Compare(a.URI, b.URI) })
	return result, nil
}

func (s *documentStore) parseBundleForValues(ctx context.Context, doc document, filename string) (*spec.UDSBundle, error) {
	uri, err := url.Parse(doc.URI)
	if err != nil {
		return nil, fmt.Errorf("parse bundle URI %q for rename: %w", doc.URI, err)
	}
	architecture, err := s.bundleArchitectureForRename(ctx, filepath.Dir(filename))
	if err != nil {
		return nil, err
	}
	parsed, diagnostics := parseBundleDocument(ctx, doc, uri, architecture)
	if parsed == nil {
		message := "bundle could not be parsed"
		if len(diagnostics) > 0 {
			message = diagnostics[0].Message
		}
		return nil, fmt.Errorf("parse bundle %q for rename: %s", filename, message)
	}
	return parsed, nil
}

func (s *documentStore) bundleArchitectureForRename(ctx context.Context, dir string) (string, error) {
	filename := filepath.Join(dir, bundle.BundleConfigFileName)
	doc, err := s.sourceDocument(filename)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read config.uds.hcl for rename: %w", err)
	}
	config, err := bundle.NewHCLParser("", iostreams.IOStreams{}).ParseBundleConfigSource(ctx, []byte(doc.Text), filename)
	if err != nil {
		return "", fmt.Errorf("resolve bundle configuration for rename: %w", err)
	}
	if config.Options == nil {
		return "", nil
	}
	return config.Options.Architecture, nil
}

func referencesValuesFile(parsed *spec.UDSBundle, bundlePath, valuesPath string) bool {
	for _, pkg := range parsed.Packages {
		for _, valuesFile := range pkg.ValuesFiles {
			if !filepath.IsAbs(valuesFile) {
				valuesFile = filepath.Join(filepath.Dir(bundlePath), valuesFile)
			}
			if filepath.Clean(valuesFile) == valuesPath {
				return true
			}
		}
	}
	return false
}

func (s *documentStore) variableShapeAtPath(path string) bundle.Variables {
	text, ok := s.documentTextAtPath(path)
	if !ok {
		return nil
	}
	file, _ := hclsyntax.ParseConfig([]byte(text), path, hcl.InitialPos)
	if file == nil {
		return nil
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	attribute, ok := body.Attributes["variables"]
	if !ok {
		return nil
	}
	object, ok := attribute.Expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil
	}
	return variablesFromObject(object)
}

func variablesFromObject(object *hclsyntax.ObjectConsExpr) bundle.Variables {
	variables := make(bundle.Variables)
	for _, item := range object.Items {
		key, diagnostics := item.KeyExpr.Value(nil)
		if diagnostics.HasErrors() || !key.IsKnown() || key.IsNull() || key.Type() != cty.String {
			continue
		}
		if nested, ok := item.ValueExpr.(*hclsyntax.ObjectConsExpr); ok {
			variables[key.AsString()] = variablesFromObject(nested)
		} else {
			variables[key.AsString()] = ""
		}
	}
	return variables
}

func (s *documentStore) documentTextAtPath(path string) (string, bool) {
	path = filepath.Clean(path)
	for _, doc := range s.documents {
		docPath, ok := fileURIPath(doc.URI)
		if ok && filepath.Clean(docPath) == path {
			return doc.Text, true
		}
	}
	text, err := os.ReadFile(path)
	return string(text), err == nil
}

func fileURIPath(uri string) (string, bool) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" || parsed.Path == "" {
		return "", false
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", false
	}
	filename := filepath.FromSlash(parsed.Path)
	if runtime.GOOS == "windows" {
		filename = strings.TrimPrefix(filename, `\`)
	}
	return filepath.Clean(filename), filepath.IsAbs(filename)
}
