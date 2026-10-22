package bundle

import (
	"reflect"
	"strings"

	"github.com/hashicorp/hcl/v2/gohcl"
)

// DefinitionField describes a tagged field in the bundle decoding structs.
// Required reflects the parser's structural requirements; domain validation
// and operation-specific constraints may impose additional requirements.
type DefinitionField struct {
	Name     string
	Type     string
	Required bool
	Repeated bool
	Labels   []string
	Fields   []DefinitionField
}

// BundleDefinitionFields derives structural editor information from the Next
// decoder. Custom-parsed content without HCL field tags is excluded.
func BundleDefinitionFields() []DefinitionField {
	return definitionFields(reflect.TypeFor[decodedBundle]())
}

func definitionFields(t reflect.Type) []DefinitionField {
	schema, _ := gohcl.ImpliedBodySchema(reflect.New(t).Interface())
	required := make(map[string]bool, len(schema.Attributes))
	for _, attr := range schema.Attributes {
		required[attr.Name] = attr.Required
	}
	labels := make(map[string][]string, len(schema.Blocks))
	for _, block := range schema.Blocks {
		labels[block.Type] = block.LabelNames
	}

	fields := make([]DefinitionField, 0, t.NumField())
	for i := range t.NumField() {
		field := t.Field(i)
		tag := field.Tag.Get("hcl")
		if tag == "" {
			continue
		}
		name, kind, _ := strings.Cut(tag, ",")
		switch kind {
		case "", "attr", "optional":
			fields = append(fields, DefinitionField{
				Name: name, Type: definitionType(field.Type), Required: required[name],
			})
		case "block":
			childType := field.Type
			for childType.Kind() == reflect.Pointer || childType.Kind() == reflect.Slice {
				childType = childType.Elem()
			}
			fields = append(fields, DefinitionField{
				Name:     name,
				Type:     "block",
				Required: field.Type.Kind() == reflect.Struct,
				Repeated: field.Type.Kind() == reflect.Slice,
				Labels:   labels[name],
				Fields:   definitionFields(childType),
			})
		}
	}
	return fields
}

func definitionType(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		return definitionType(t.Elem())
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "list(" + definitionType(t.Elem()) + ")"
	case reflect.Map:
		return "map(" + definitionType(t.Elem()) + ")"
	default:
		return "object"
	}
}
