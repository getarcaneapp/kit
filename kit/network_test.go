package kit

import (
	"slices"
	"testing"
)

func TestCompareAddresses(t *testing.T) {
	t.Parallel()

	got := []string{"10.0.0.10", "bogus", "", "10.0.0.2/24", "::1", "192.168.1.1", "alpha"}
	slices.SortFunc(got, CompareAddresses)
	want := []string{"", "10.0.0.2/24", "10.0.0.10", "192.168.1.1", "::1", "alpha", "bogus"}
	if !slices.Equal(got, want) {
		t.Errorf("sorted by CompareAddresses = %q, want %q", got, want)
	}
}

func TestCompareSubnets(t *testing.T) {
	t.Parallel()

	got := []string{"10.0.0.0/16", "not-a-cidr", "", "10.0.0.0/24", "10.0.1.7/24", "172.16.0.0/12", "fd00::/8", "10.0.0.0/8"}
	slices.SortFunc(got, CompareSubnets)
	want := []string{"", "10.0.0.0/8", "10.0.0.0/16", "10.0.0.0/24", "10.0.1.7/24", "172.16.0.0/12", "fd00::/8", "not-a-cidr"}
	if !slices.Equal(got, want) {
		t.Errorf("sorted by CompareSubnets = %q, want %q", got, want)
	}

	if CompareSubnets("10.0.0.5/24", "10.0.0.9/24") != 0 {
		t.Error("prefixes with the same masked network should compare equal")
	}
}
