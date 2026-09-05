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
	p, err := planInternal(v.Type())
	if err != nil {
		return err
	}
	return normalizeValueInternal(v, p, make(map[any]bool))
}

// Text optionally trims surrounding Unicode whitespace, then applies NFC.
// It preserves case and internal whitespace and must not be used for secrets.
func Text(value string, trim, nfc bool) string {
	if trim {
		value = strings.TrimSpace(value)
	}
	if nfc {
		value = norm.NFC.String(value)
	}
	return value
}

func normalizeValueInternal(v reflect.Value, p *plan, seen map[any]bool) error {
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
		return normalizeValueInternal(v.Elem(), p.element, seen)
	}

	switch {
	case v.Kind() == reflect.Struct:
		for _, f := range p.fields {
			if err := normalizeFieldInternal(v.Field(f.index), f, seen); err != nil {
				return fmt.Errorf("%s: %w", f.name, err)
			}
		}
	case v.Kind() == reflect.Slice || v.Kind() == reflect.Array:
		for i := range v.Len() {
			if err := normalizeValueInternal(v.Index(i), p.element, seen); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
	}
	return nil
}

func normalizeFieldInternal(value reflect.Value, field fieldPlan, seen map[any]bool) error {
	if !field.trim && !field.nfc {
		return normalizeValueInternal(value, field.plan, seen)
	}
	if !value.CanSet() {
		return nil
	}
	normalized, err := normalizedStringInternal(value, field)
	if err != nil {
		return err
	}
	value.Set(normalized)
	return nil
}

func normalizedStringInternal(value reflect.Value, field fieldPlan) (reflect.Value, error) {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return value, nil
		}
		normalized, err := normalizedStringInternal(value.Elem(), field)
		if err != nil {
			return reflect.Value{}, err
		}
		replacement := reflect.New(value.Type().Elem())
		replacement.Elem().Set(normalized)
		return replacement, nil
	}
	text := Text(value.String(), field.trim, field.nfc)
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
