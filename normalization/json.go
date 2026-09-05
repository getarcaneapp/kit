package normalization

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
)

// NormalizeJSON normalizes tagged JSON strings before schema validation.
// Raw JSON values retain their types and precision; unknown fields are preserved.
// Invalid JSON returns an error so the caller can retain its normal error handling.
func NormalizeJSON(data []byte, typ reflect.Type) ([]byte, error) {
	p, err := planInternal(typ)
	if err != nil {
		return nil, err
	}
	if !p.hasRules {
		return data, nil
	}
	var raw jsontext.Value
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return normalizeJSONInternal(data, p)
}

func normalizeJSONInternal(data []byte, p *plan) ([]byte, error) {
	if !p.hasRules {
		return data, nil
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return data, nil
	}
	if p.kind == reflect.Pointer {
		return normalizeJSONInternal(data, p.element)
	}
	if p.kind == reflect.Struct {
		if data[0] != '{' {
			return data, nil
		}
		fields := make(map[string]jsontext.Value)
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		if err := normalizeObjectInternal(fields, p); err != nil {
			return nil, err
		}
		return json.Marshal(fields)
	}
	if p.kind == reflect.Slice || p.kind == reflect.Array {
		if data[0] != '[' {
			return data, nil
		}
		var values []jsontext.Value
		if err := json.Unmarshal(data, &values); err != nil {
			return nil, err
		}
		for i, value := range values {
			normalized, err := normalizeJSONInternal(value, p.element)
			if err != nil {
				return nil, err
			}
			values[i] = normalized
		}
		return json.Marshal(values)
	}
	return data, nil
}

func normalizeObjectInternal(fields map[string]jsontext.Value, p *plan) error {
	for _, f := range p.fields {
		child := f.plan
		for child.kind == reflect.Pointer {
			child = child.element
		}
		if f.inline && child.kind == reflect.Struct {
			if err := normalizeObjectInternal(fields, child); err != nil {
				return err
			}
			continue
		}
		value, ok := fields[f.name]
		if !ok {
			continue
		}
		if f.trim || f.nfc {
			if value.Kind() != '"' {
				continue
			}
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				return err
			}
			normalized, err := json.Marshal(Text(text, f.trim, f.nfc))
			if err != nil {
				return err
			}
			fields[f.name] = normalized
			continue
		}
		normalized, err := normalizeJSONInternal(value, f.plan)
		if err != nil {
			return err
		}
		fields[f.name] = normalized
	}
	return nil
}
