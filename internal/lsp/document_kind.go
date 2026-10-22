package lsp

import (
	"net/url"
	"path"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
)

func definitionFieldsForDocument(uri string) ([]bundle.DefinitionField, bool) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, false
	}
	switch path.Base(parsed.Path) {
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
