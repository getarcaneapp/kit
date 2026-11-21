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
	parseConverter(netip.ParsePrefix),
	parseConverter(netip.ParseAddr),
	formatConverter[netip.Prefix](),
	formatConverter[netip.Addr](),
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

func parseConverter[T any](parse func(string) (T, error)) copier.TypeConverter {
	return converter(func(raw string) (T, error) {
		if raw = strings.TrimSpace(raw); raw == "" {
			var zero T
			return zero, nil
		}
		return parse(raw)
	})
}

func formatConverter[T interface {
	IsValid() bool
	String() string
}]() copier.TypeConverter {
	return converter(func(value T) (string, error) {
		if !value.IsValid() {
			return "", nil
		}
		return value.String(), nil
	})
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
