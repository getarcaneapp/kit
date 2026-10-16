// Package mapping deep-copies between structurally similar types, converting
// netip values to and from their string form along the way.
package mapping

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/jinzhu/copier"
)

var typeConverters = []copier.TypeConverter{
	converter(func(raw string) (netip.Prefix, error) {
		if raw = strings.TrimSpace(raw); raw == "" {
			return netip.Prefix{}, nil
		}
		return netip.ParsePrefix(raw)
	}),
	converter(func(raw string) (netip.Addr, error) {
		if raw = strings.TrimSpace(raw); raw == "" {
			return netip.Addr{}, nil
		}
		return netip.ParseAddr(raw)
	}),
	converter(func(prefix netip.Prefix) (string, error) {
		if !prefix.IsValid() {
			return "", nil
		}
		return prefix.String(), nil
	}),
	converter(func(addr netip.Addr) (string, error) {
		if !addr.IsValid() {
			return "", nil
		}
		return addr.String(), nil
	}),
}

// MapStruct deep-copies source into destination.
func MapStruct[S, D any](source S, destination *D) error {
	return copier.CopyWithOption(destination, source, copier.Option{DeepCopy: true, Converters: typeConverters})
}

// MapOne deep-copies source into a new D.
func MapOne[S, D any](source S) (D, error) {
	var dest D
	err := MapStruct(source, &dest)
	return dest, err
}

// MapSlice deep-copies every element of source into a new []D.
func MapSlice[S, D any](source []S) ([]D, error) {
	dest := make([]D, len(source))
	for i := range source {
		if err := MapStruct(source[i], &dest[i]); err != nil {
			return nil, fmt.Errorf("map item %d: %w", i, err)
		}
	}
	return dest, nil
}

// converter adapts a typed conversion into a copier converter.
func converter[S, D any](convert func(S) (D, error)) copier.TypeConverter {
	var src S
	var dst D
	return copier.TypeConverter{
		SrcType: src,
		DstType: dst,
		Fn: func(value any) (any, error) {
			typed, ok := value.(S)
			if !ok {
				return nil, fmt.Errorf("expected %T, got %T", src, value)
			}
			return convert(typed)
		},
	}
}
