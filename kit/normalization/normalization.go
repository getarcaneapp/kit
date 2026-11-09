// Package normalization applies opt-in Unicode NFC and whitespace normalization.
package normalization

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

type (
	// TextOptions selects the normalizations Text applies. The zero value returns
	// the input unchanged.
	TextOptions struct {
		// Trim removes leading and trailing whitespace.
		Trim bool
		// NFC converts the text to Unicode Normalization Form C.
		NFC bool
	}
)

// Normalize normalizes tagged fields in place and checks their length constraints.
// The input must be a non-nil pointer. Untagged strings, maps, and nested nil
// pointers are left untouched. Tagged string
// pointers are replaced so normalization does not modify aliased pointees.
func Normalize(value any) error {
	if value == nil {
		return errors.New("normalization requires a non-nil pointer")
	}
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Pointer {
		return fmt.Errorf("normalization requires a pointer, got %T", value)
	}
	if v.IsNil() {
		return errors.New("normalization requires a non-nil pointer")
	}
	p, err := planFor(v.Type())
	if err != nil {
		return err
	}
	return normalizeValue(v, p, make(map[any]bool))
}

// Text applies the normalizations selected by opts: trimming first, then NFC.
// It preserves case and internal whitespace and must not be used for secrets.
func Text(value string, opts TextOptions) string {
	if opts.Trim {
		value = strings.TrimSpace(value)
	}
	if opts.NFC {
		value = norm.NFC.String(value)
	}
	return value
}

func normalizeValue(v reflect.Value, p *plan, seen map[any]bool) error {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		key := v.Interface()
		if seen[key] {
			return nil
		}
		seen[key] = true
		defer delete(seen, key)
		return normalizeValue(v.Elem(), p.element, seen)
	}

	switch {
	case v.Kind() == reflect.Struct:
		for _, f := range p.fields {
			field := v.Field(f.index)
			if f.text == (TextOptions{}) {
				if err := normalizeValue(field, f.plan, seen); err != nil {
					return fmt.Errorf("%s: %w", f.name, err)
				}
				continue
			}
			if !field.CanSet() {
				continue
			}
			normalized, err := normalizedString(field, f)
			if err != nil {
				return fmt.Errorf("%s: %w", f.name, err)
			}
			field.Set(normalized)
		}
	case v.Kind() == reflect.Slice || v.Kind() == reflect.Array:
		for i := range v.Len() {
			if err := normalizeValue(v.Index(i), p.element, seen); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
	}
	return nil
}

func normalizedString(value reflect.Value, field fieldPlan) (reflect.Value, error) {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return value, nil
		}
		normalized, err := normalizedString(value.Elem(), field)
		if err != nil {
			return reflect.Value{}, err
		}
		replacement := reflect.New(value.Type().Elem())
		replacement.Elem().Set(normalized)
		return replacement, nil
	}
	text := Text(value.String(), field.text)
	length := utf8.RuneCountInString(text)
	if length < field.minLength {
		return reflect.Value{}, fmt.Errorf("must contain at least %d characters after normalization", field.minLength)
	}
	if field.maxLength >= 0 && length > field.maxLength {
		return reflect.Value{}, fmt.Errorf("must contain at most %d characters after normalization", field.maxLength)
	}
	replacement := reflect.New(value.Type()).Elem()
	replacement.SetString(text)
	return replacement, nil
}
