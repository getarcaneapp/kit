package kit

import "reflect"

// AsStringMap converts any map with string or interface keys, including named map types,
// to map[string]any. Entries whose key is not a string are dropped. The second result is
// false when value is not such a map.
func AsStringMap(value any) (map[string]any, bool) {
	if m, ok := value.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Map {
		return nil, false
	}
	if keyKind := rv.Type().Key().Kind(); keyKind != reflect.String && keyKind != reflect.Interface {
		return nil, false
	}
	out := make(map[string]any, rv.Len())
	for iter := rv.MapRange(); iter.Next(); {
		key := iter.Key()
		if key.Kind() == reflect.Interface {
			key = key.Elem()
		}
		if key.Kind() != reflect.String {
			continue
		}
		out[key.String()] = iter.Value().Interface()
	}
	return out, true
}
