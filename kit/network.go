package kit

import (
	"cmp"
	"net/netip"
	"strings"
)

// CompareAddresses orders IP address strings numerically, ignoring any CIDR suffix.
// Empty values sort first and malformed values last in string order.
func CompareAddresses(a, b string) int {
	return compareParsed(a, b, func(raw string) (netip.Addr, error) {
		host, _, _ := strings.Cut(raw, "/")
		return netip.ParseAddr(host)
	}, netip.Addr.Compare)
}

// CompareSubnets orders CIDR strings by masked network address, then prefix length.
// Empty values sort first and malformed values last in string order.
func CompareSubnets(a, b string) int {
	return compareParsed(a, b, netip.ParsePrefix, func(pa, pb netip.Prefix) int {
		return cmp.Or(pa.Masked().Addr().Compare(pb.Masked().Addr()), cmp.Compare(pa.Bits(), pb.Bits()))
	})
}

func compareParsed[T any](a, b string, parse func(string) (T, error), compare func(T, T) int) int {
	if a == "" || b == "" {
		return strings.Compare(a, b)
	}
	parsedA, errA := parse(a)
	parsedB, errB := parse(b)
	switch {
	case errA == nil && errB == nil:
		return compare(parsedA, parsedB)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}
