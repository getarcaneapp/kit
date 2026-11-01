package kit

import "reflect"

// Unique returns the distinct items in first-seen order, or nil when items is empty.
func Unique[S ~[]E, E comparable](items S) S {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[E]struct{}, len(items))
	out := make(S, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// Collect maps value through mapper, treating any slice or array as its elements and
// anything else (including []byte) as a single item. A nil value yields nil.
func Collect[T any](value any, mapper func(any) T) []T {
	if value == nil {
		return nil
	}
	rv := reflect.ValueOf(value)
	kind := rv.Kind()
	if (kind != reflect.Slice && kind != reflect.Array) || rv.Type().Elem().Kind() == reflect.Uint8 {
		return []T{mapper(value)}
	}
	out := make([]T, rv.Len())
	for i := range rv.Len() {
		out[i] = mapper(rv.Index(i).Interface())
	}
	return out
}
