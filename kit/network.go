package kit

import (
	"cmp"
	"net/netip"
	"strings"
)

// CompareAddresses orders IP address strings numerically, ignoring any CIDR suffix.
// Empty values sort first and malformed values last in string order.
func CompareAddresses(a, b string) int {
	if a == "" || b == "" {
		return strings.Compare(a, b)
	}
	hostA, _, _ := strings.Cut(a, "/")
	hostB, _, _ := strings.Cut(b, "/")
	addrA, errA := netip.ParseAddr(hostA)
	addrB, errB := netip.ParseAddr(hostB)
	switch {
	case errA == nil && errB == nil:
		return addrA.Compare(addrB)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// CompareSubnets orders CIDR strings by masked network address, then prefix length.
// Empty values sort first and malformed values last in string order.
func CompareSubnets(a, b string) int {
	if a == "" || b == "" {
		return strings.Compare(a, b)
	}
	prefixA, errA := netip.ParsePrefix(a)
	prefixB, errB := netip.ParsePrefix(b)
	switch {
	case errA == nil && errB == nil:
		return cmp.Or(prefixA.Masked().Addr().Compare(prefixB.Masked().Addr()), cmp.Compare(prefixA.Bits(), prefixB.Bits()))
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}
