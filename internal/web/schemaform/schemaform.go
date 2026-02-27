package schemaform

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// This package provides a small subset of JSON Schema -> form model.
//
// Scope (intentionally small):
// - root schema must be {"type":"object"}
// - properties: string/number/integer/boolean
// - required: ["a","b"]
// - field defaults from schema properties, plus plugin default object
//
// Not supported yet: nested objects, arrays, oneOf/anyOf/allOf, refs.

type FieldType string

const (
	FieldString  FieldType = "string"
	FieldNumber  FieldType = "number"
	FieldInteger FieldType = "integer"
	FieldBoolean FieldType = "boolean"
)

type Field struct {
	Name        string
	Title       string
	Description string
	Type        FieldType
	Required    bool
	Default     any
}

type Form struct {
	Title       string
	Description string
	Fields      []Field
}

func Parse(schema json.RawMessage) (Form, error) {
	if len(schema) == 0 {
		return Form{}, errors.New("schema is empty")
	}

	var root map[string]any
	if err := json.Unmarshal(schema, &root); err != nil {
		return Form{}, fmt.Errorf("invalid schema json: %w", err)
	}

	typeVal, _ := root["type"].(string)
	if typeVal == "" {
		typeVal = "object"
	}
	if typeVal != "object" {
		return Form{}, fmt.Errorf("only root type=object supported, got %q", typeVal)
	}

	f := Form{}
	f.Title, _ = root["title"].(string)
	f.Description, _ = root["description"].(string)

	requiredSet := map[string]bool{}
	if req, ok := root["required"].([]any); ok {
		for _, v := range req {
			if s, ok := v.(string); ok {
				requiredSet[s] = true
			}
		}
	}

	props, _ := root["properties"].(map[string]any)
	if len(props) == 0 {
		return f, nil
	}

	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		rawProp := props[name]
		pm, ok := rawProp.(map[string]any)
		if !ok {
			continue
		}

		t, _ := pm["type"].(string)
		ft := FieldType(t)
		switch ft {
		case FieldString, FieldNumber, FieldInteger, FieldBoolean:
			// ok
		default:
			continue
		}

		title, _ := pm["title"].(string)
		desc, _ := pm["description"].(string)
		def, _ := pm["default"]

		f.Fields = append(f.Fields, Field{
			Name:        name,
			Title:       title,
			Description: desc,
			Type:        ft,
			Required:    requiredSet[name],
			Default:     def,
		})
	}

	return f, nil
}

// ApplyDefaults builds a config object by merging:
// 1) schema field defaults
// 2) pluginDefault object (if any)
// 3) current config object (if any)
// Later sources override earlier ones.
func ApplyDefaults(current json.RawMessage, pluginDefault json.RawMessage, schema Form) (map[string]any, error) {
	out := map[string]any{}

	// 1) schema defaults
	for _, field := range schema.Fields {
		if field.Default != nil {
			out[field.Name] = field.Default
		}
	}

	mergeObj := func(raw json.RawMessage) {
		if len(raw) == 0 {
			return
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return
		}
		for k, v := range m {
			out[k] = v
		}
	}

	// 2) plugin default
	mergeObj(pluginDefault)
	// 3) current
	mergeObj(current)

	return out, nil
}

// CoerceFromForm reads POSTed form values and coerces them to typed values.
// Only fields declared in schema are accepted.
// For booleans: missing means false (unchecked).
func CoerceFromForm(values map[string][]string, schema Form) (map[string]any, error) {
	out := map[string]any{}
	fieldBy := map[string]Field{}
	for _, f := range schema.Fields {
		fieldBy[f.Name] = f
	}

	// init booleans
	for name, field := range fieldBy {
		if field.Type == FieldBoolean {
			_, ok := values[name]
			out[name] = ok
		}
	}

	for k, vs := range values {
		if len(vs) == 0 {
			continue
		}
		field, ok := fieldBy[k]
		if !ok {
			continue
		}
		v := strings.TrimSpace(vs[0])

		switch field.Type {
		case FieldString:
			out[k] = v
		case FieldNumber:
			num, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("field %s: invalid number", k)
			}
			out[k] = num
		case FieldInteger:
			num, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("field %s: invalid integer", k)
			}
			out[k] = num
		case FieldBoolean:
			// already handled
		}
	}

	// required check
	for _, f := range schema.Fields {
		if !f.Required {
			continue
		}
		v, ok := out[f.Name]
		if !ok {
			return nil, fmt.Errorf("missing required field: %s", f.Name)
		}
		if f.Type == FieldString {
			if s, _ := v.(string); strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("missing required field: %s", f.Name)
			}
		}
	}

	return out, nil
}
