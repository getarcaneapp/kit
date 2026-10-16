package normalization

import (
	"cmp"
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
	p, err := planFor(typ)
	if err != nil {
		return false, err
	}
	return p.hasRules, nil
}

func planFor(typ reflect.Type) (*plan, error) {
	if typ == nil {
		return nil, errors.New("normalization type is nil")
	}
	plans.Lock()
	defer plans.Unlock()
	if cached, ok := plans.values[typ]; ok {
		return cached.plan, cached.err
	}
	seen := make(map[reflect.Type]*plan)
	p, err := compile(typ, seen)
	if err == nil {
		for nodeType, node := range seen {
			if err = validateInline(node, make(map[*plan]bool)); err != nil {
				err = fmt.Errorf("%s: %w", nodeType, err)
				break
			}
			*node = plan{
				kind:     node.kind,
				element:  node.element,
				fields:   node.fields,
				hasRules: hasRules(node, make(map[*plan]bool)),
			}
		}
	}
	plans.values[typ] = cachedPlan{plan: p, err: err}
	return p, err
}

func compile(typ reflect.Type, seen map[reflect.Type]*plan) (*plan, error) {
	if p, ok := seen[typ]; ok {
		return p, nil
	}
	p := &plan{kind: typ.Kind()}
	seen[typ] = p

	switch {
	case typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array:
		child, err := compile(typ.Elem(), seen)
		if err != nil {
			return nil, err
		}
		*p = plan{kind: typ.Kind(), element: child}
	case typ.Kind() == reflect.Struct:
		var fields []fieldPlan
		for i := range typ.NumField() {
			field, include, err := compileField(typ.Field(i), seen)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", typ, typ.Field(i).Name, err)
			}
			if include {
				fields = append(fields, field)
			}
		}
		*p = plan{kind: typ.Kind(), fields: fields}
	}
	return p, nil
}

func compileField(field reflect.StructField, seen map[reflect.Type]*plan) (fieldPlan, bool, error) {
	trim, hasTrim := field.Tag.Lookup("trim")
	unorm, hasNorm := field.Tag.Lookup("unorm")
	if hasTrim && trim != "true" {
		return fieldPlan{}, false, fmt.Errorf("unsupported trim tag %q", trim)
	}
	if hasNorm && unorm != "nfc" {
		return fieldPlan{}, false, fmt.Errorf("unsupported unorm tag %q", unorm)
	}
	fieldType := field.Type
	for fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	minimum, maximum := 0, -1
	if hasTrim || hasNorm {
		if fieldType.Kind() != reflect.String || !field.IsExported() {
			return fieldPlan{}, false, errors.New("normalization tags require an exported string or string pointer")
		}
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
				return fieldPlan{}, false, fmt.Errorf("%s must be a nonnegative integer, got %q", constraint.name, raw)
			}
			*constraint.target = length
		}
		if maximum >= 0 && minimum > maximum {
			return fieldPlan{}, false, errors.New("minLength exceeds maxLength")
		}
	}
	jsonTag := strings.Split(field.Tag.Get("json"), ",")
	if !field.IsExported() || jsonTag[0] == "-" {
		return fieldPlan{}, false, nil
	}
	child, err := compile(field.Type, seen)
	return fieldPlan{
		index:     field.Index[0],
		name:      cmp.Or(jsonTag[0], field.Name),
		inline:    (field.Anonymous && jsonTag[0] == "" && fieldType.Kind() == reflect.Struct) || slices.Contains(jsonTag[1:], "inline"),
		trim:      hasTrim,
		nfc:       hasNorm,
		plan:      child,
		minLength: minimum,
		maxLength: maximum,
	}, true, err
}

func hasRules(p *plan, seen map[*plan]bool) bool {
	if p == nil || seen[p] {
		return false
	}
	seen[p] = true
	for _, f := range p.fields {
		if f.trim || f.nfc || hasRules(f.plan, seen) {
			return true
		}
	}
	return hasRules(p.element, seen)
}

func validateInline(p *plan, ancestors map[*plan]bool) error {
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
			if err := validateInline(f.plan, ancestors); err != nil {
				return err
			}
		}
	}
	return nil
}
