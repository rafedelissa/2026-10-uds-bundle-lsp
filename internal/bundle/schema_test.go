package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundleDefinitionFields(t *testing.T) {
	fields := BundleDefinitionFields()
	uds := definitionField(t, fields, "uds")
	assert.Equal(t, "block", uds.Type)
	assert.True(t, uds.Required)
	assert.Equal(t, DefinitionField{Name: "bundle_api_version", Type: "string", Required: true}, definitionField(t, uds.Fields, "bundle_api_version"))

	metadata := definitionField(t, fields, "metadata")
	assert.True(t, definitionField(t, metadata.Fields, "name").Required)
	assert.False(t, definitionField(t, metadata.Fields, "description").Required)

	pkg := definitionField(t, fields, "package")
	assert.Equal(t, []string{"name"}, pkg.Labels)
	assert.True(t, pkg.Repeated)
	assert.False(t, pkg.Required, "package count is validated by domain rules, not the decoder")
	assert.Equal(t, DefinitionField{Name: "source", Type: "string", Required: true}, definitionField(t, pkg.Fields, "source"))
	assert.Equal(t, DefinitionField{Name: "values_files", Type: "list(string)"}, definitionField(t, pkg.Fields, "values_files"))
	assert.Equal(t, DefinitionField{Name: "depends_on", Type: "list(package reference)"}, definitionField(t, pkg.Fields, "depends_on"))
	verification := definitionField(t, pkg.Fields, "signature_verification")
	assert.False(t, verification.Required)
	assert.Equal(t, DefinitionField{Name: "verify", Type: "bool"}, definitionField(t, verification.Fields, "verify"))
	keyless := definitionField(t, verification.Fields, "keyless")
	assert.Equal(t, "bool", definitionField(t, keyless.Fields, "insecure_ignore_tlog").Type)
	assert.Equal(t, "string", definitionField(t, keyless.Fields, "certificate_identity").Type)

	locals := definitionField(t, fields, "locals")
	assert.Equal(t, "block", locals.Type)
	assert.True(t, locals.Repeated)

	for _, field := range pkg.Fields {
		assert.NotEqual(t, "DependsOn", field.Name)
		assert.NotEqual(t, "Remain", field.Name)
	}
}

func TestConfigDefinitionFields(t *testing.T) {
	fields := ConfigDefinitionFields()

	options := definitionField(t, fields, "options")
	assert.Equal(t, "block", options.Type)
	assert.Equal(t, "string", definitionField(t, options.Fields, "log_level").Type)
	assert.Equal(t, "number", definitionField(t, options.Fields, "concurrency").Type)

	verification := definitionField(t, fields, "signature_verification")
	keyless := definitionField(t, verification.Fields, "keyless")
	assert.Equal(t, "string", definitionField(t, keyless.Fields, "trusted_root").Type)
	assert.Equal(t, DefinitionField{Name: "variables", Type: "object"}, definitionField(t, fields, "variables"))
}

func TestDefaultsDefinitionFields(t *testing.T) {
	assert.Equal(t,
		[]DefinitionField{{Name: "variables", Type: "object"}},
		DefaultsDefinitionFields(),
	)
}

func definitionField(t *testing.T, fields []DefinitionField, name string) DefinitionField {
	t.Helper()
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	require.FailNow(t, "missing schema field", "field: %s", name)
	return DefinitionField{}
}
