package lsp

import (
	"net/url"
	"path"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
)

func definitionFieldsForDocument(uri string) ([]bundle.DefinitionField, bool) {
	name := udsHCLFileName(uri)
	if name == "" {
		return nil, false
	}
	switch name {
	case bundle.BundleFileName:
		return bundle.BundleDefinitionFields(), true
	case bundle.BundleConfigFileName:
		return bundle.ConfigDefinitionFields(), false
	case bundle.BundleDefaultsFileName:
		return bundle.DefaultsDefinitionFields(), false
	default:
		return nil, false
	}
}

func udsHCLFileName(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	name := path.Base(parsed.Path)
	switch name {
	case bundle.BundleFileName, bundle.BundleConfigFileName, bundle.BundleDefaultsFileName:
		return name
	default:
		return ""
	}
}
