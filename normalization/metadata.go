package normalization

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
)

type fieldPlan struct {
	index     int
	name      string
	inline    bool
	trim      bool
	nfc       bool
	plan      *plan
	minLength int
	maxLength int
}

type plan struct {
	kind     reflect.Kind
	element  *plan
	fields   []fieldPlan
	hasRules bool
}

type cachedPlan struct {
	plan *plan
	err  error
}

var plans = struct {
	sync.Mutex

	values map[reflect.Type]cachedPlan
}{values: make(map[reflect.Type]cachedPlan)}

// HasRules validates normalization tags and reports whether the type contains any.
// Compiled type metadata is cached and safe for concurrent use.
func HasRules(typ reflect.Type) (bool, error) {
	p, err := planInternal(typ)
	if err != nil {
		return false, err
	}
	return p.hasRules, nil
}

func planInternal(typ reflect.Type) (*plan, error) {
	if typ == nil {
		return nil, errors.New("normalization type is nil")
	}
	plans.Lock()
	defer plans.Unlock()
	if cached, ok := plans.values[typ]; ok {
		return cached.plan, cached.err
	}
	seen := make(map[reflect.Type]*plan)
	p, err := compileInternal(typ, seen)
	if err == nil {
		for nodeType, node := range seen {
			if err = validateInlineInternal(node, make(map[*plan]bool)); err != nil {
				err = fmt.Errorf("%s: %w", nodeType, err)
				break
			}
			node.hasRules = hasRulesInternal(node, make(map[*plan]bool))
		}
	}
	plans.values[typ] = cachedPlan{plan: p, err: err}
	return p, err
}

func compileInternal(typ reflect.Type, seen map[reflect.Type]*plan) (*plan, error) {
	if p, ok := seen[typ]; ok {
		return p, nil
	}
	p := &plan{kind: typ.Kind()}
	seen[typ] = p

	switch {
	case typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array:
		child, err := compileInternal(typ.Elem(), seen)
		if err != nil {
			return nil, err
		}
		p.element = child
	case typ.Kind() == reflect.Struct:
		for i := range typ.NumField() {
			field, include, err := compileFieldInternal(typ.Field(i), seen)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", typ, typ.Field(i).Name, err)
			}
			if include {
				field.index = i
				p.fields = append(p.fields, field)
			}
		}
	}
	return p, nil
}

func compileFieldInternal(field reflect.StructField, seen map[reflect.Type]*plan) (fieldPlan, bool, error) {
	result, err := compileRulesInternal(field)
	if err != nil {
		return result, false, err
	}
	jsonTag := strings.Split(field.Tag.Get("json"), ",")
	if !field.IsExported() || jsonTag[0] == "-" {
		return result, false, nil
	}
	fieldType := field.Type
	for fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	result.name = jsonTag[0]
	result.inline = field.Anonymous && result.name == "" && fieldType.Kind() == reflect.Struct
	result.inline = result.inline || slices.Contains(jsonTag[1:], "inline")
	if result.name == "" {
		result.name = field.Name
	}
	result.plan, err = compileInternal(field.Type, seen)
	return result, true, err
}

func compileRulesInternal(field reflect.StructField) (fieldPlan, error) {
	result := fieldPlan{maxLength: -1}
	trim, hasTrim := field.Tag.Lookup("trim")
	unorm, hasNorm := field.Tag.Lookup("unorm")
	if hasTrim && trim != "true" {
		return result, fmt.Errorf("unsupported trim tag %q", trim)
	}
	if hasNorm && unorm != "nfc" {
		return result, fmt.Errorf("unsupported unorm tag %q", unorm)
	}
	if !hasTrim && !hasNorm {
		return result, nil
	}
	fieldType := field.Type
	for fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	if fieldType.Kind() != reflect.String || !field.IsExported() {
		return result, errors.New("normalization tags require an exported string or string pointer")
	}
	result.trim, result.nfc = hasTrim, hasNorm
	var err error
	result.minLength, result.maxLength, err = compileLengthsInternal(field)
	return result, err
}

func hasRulesInternal(p *plan, seen map[*plan]bool) bool {
	if p == nil || seen[p] {
		return false
	}
	seen[p] = true
	for _, f := range p.fields {
		if f.trim || f.nfc || hasRulesInternal(f.plan, seen) {
			return true
		}
	}
	return hasRulesInternal(p.element, seen)
}

func validateInlineInternal(p *plan, ancestors map[*plan]bool) error {
	for p.kind == reflect.Pointer {
		p = p.element
	}
	if p.kind != reflect.Struct {
		return nil
	}
	if ancestors[p] {
		return errors.New("recursive inline JSON fields are unsupported")
	}
	ancestors[p] = true
	defer delete(ancestors, p)
	for _, f := range p.fields {
		if f.inline {
			if err := validateInlineInternal(f.plan, ancestors); err != nil {
				return err
			}
		}
	}
	return nil
}

func compileLengthsInternal(field reflect.StructField) (int, int, error) {
	minimum, maximum := 0, -1
	for _, constraint := range []struct {
		name   string
		target *int
	}{{name: "minLength", target: &minimum}, {name: "maxLength", target: &maximum}} {
		raw, ok := field.Tag.Lookup(constraint.name)
		if !ok {
			continue
		}
		length, err := strconv.Atoi(raw)
		if err != nil || length < 0 {
			return 0, 0, fmt.Errorf("%s must be a nonnegative integer, got %q", constraint.name, raw)
		}
		*constraint.target = length
	}
	if maximum >= 0 && minimum > maximum {
		return 0, 0, errors.New("minLength exceeds maxLength")
	}
	return minimum, maximum, nil
}
