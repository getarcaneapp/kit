package kit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"
)

// SHA256Hex returns the hex-encoded SHA-256 digest of s.
func SHA256Hex[T string | []byte](s T) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// WriteRecord writes fields NUL-separated and newline-terminated, for stable content hashes.
func WriteRecord(w io.Writer, fields ...any) {
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = fmt.Sprint(field)
	}
	_, _ = io.WriteString(w, strings.Join(parts, "\x00")+"\n")
}

// Fingerprint returns an order-sensitive FNV-1a hash of values for change
// detection. Strings are NUL-terminated, integers varint-encoded, time.Time
// hashed by UnixNano, nil pointers tagged absent and non-nil ones present plus
// the value, slices and arrays prefixed by length, structs walked by exported
// field, and maps hashed in sorted key order. It is not collision resistant and
// must never back a security decision.
func Fingerprint(values ...any) uint64 {
	h := fnv.New64a()
	var buf [binary.MaxVarintLen64]byte
	writeInt := func(v int64) { _, _ = h.Write(buf[:binary.PutVarint(buf[:], v)]) }
	var write func(reflect.Value)
	write = func(v reflect.Value) {
		if !v.IsValid() {
			writeInt(0)
			return
		}
		if t, ok := reflect.TypeAssert[time.Time](v); ok {
			writeInt(t.UnixNano())
			return
		}
		//exhaustive:ignore
		switch v.Kind() {
		case reflect.String:
			_, _ = h.Write([]byte(v.String()))
			_, _ = h.Write([]byte{0})
		case reflect.Bool:
			writeInt(Ternary[int64](v.Bool(), 1, 0))
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			writeInt(v.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			_, _ = h.Write(buf[:binary.PutUvarint(buf[:], v.Uint())])
		case reflect.Pointer, reflect.Interface:
			writeInt(Ternary[int64](v.IsNil(), 0, 1))
			if !v.IsNil() {
				write(v.Elem())
			}
		case reflect.Slice, reflect.Array:
			writeInt(int64(v.Len()))
			for i := range v.Len() {
				write(v.Index(i))
			}
		case reflect.Struct:
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					write(v.Field(i))
				}
			}
		case reflect.Map:
			keys := v.MapKeys()
			slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
			writeInt(int64(len(keys)))
			for _, key := range keys {
				write(key)
				write(v.MapIndex(key))
			}
		default:
			_, _ = fmt.Fprint(h, v.Interface())
			_, _ = h.Write([]byte{0})
		}
	}
	for _, value := range values {
		write(reflect.ValueOf(value))
	}
	return h.Sum64()
}
