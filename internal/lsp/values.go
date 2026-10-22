package lsp

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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
