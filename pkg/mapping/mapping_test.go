package mapping

import (
	"net/netip"
	"testing"
)

type netipConfig struct {
	Subnet  netip.Prefix
	Gateway netip.Addr
	Aux     map[string]netip.Addr
}

type stringConfig struct {
	Subnet  string
	Gateway string
	Aux     map[string]string
}

func TestMapOneConvertsNetipAndStrings(t *testing.T) {
	t.Parallel()

	source := netipConfig{
		Subnet:  netip.MustParsePrefix("172.16.5.0/24"),
		Gateway: netip.MustParseAddr("172.16.5.1"),
		Aux:     map[string]netip.Addr{"dns": netip.MustParseAddr("172.16.5.53"), "unset": {}},
	}

	mapped, err := MapOne[netipConfig, stringConfig](source)
	if err != nil {
		t.Fatalf("MapOne: %v", err)
	}
	if mapped.Subnet != "172.16.5.0/24" || mapped.Gateway != "172.16.5.1" {
		t.Errorf("mapped = %+v, want string forms of the netip values", mapped)
	}
	if mapped.Aux["dns"] != "172.16.5.53" || mapped.Aux["unset"] != "" {
		t.Errorf("Aux = %v, want dns converted and unset empty", mapped.Aux)
	}

	back, err := MapOne[stringConfig, netipConfig](stringConfig{Subnet: " 10.0.0.0/8 ", Gateway: "", Aux: map[string]string{"x": "10.0.0.1"}})
	if err != nil {
		t.Fatalf("MapOne back: %v", err)
	}
	if back.Subnet != netip.MustParsePrefix("10.0.0.0/8") || back.Gateway.IsValid() || back.Aux["x"] != netip.MustParseAddr("10.0.0.1") {
		t.Errorf("back = %+v, want parsed netip values with empty gateway", back)
	}

	if _, err = MapOne[stringConfig, netipConfig](stringConfig{Subnet: "not-a-prefix"}); err == nil {
		t.Error("MapOne accepted an invalid prefix")
	}
}

func TestMapSlice(t *testing.T) {
	t.Parallel()

	got, err := MapSlice[netipConfig, stringConfig]([]netipConfig{{Gateway: netip.MustParseAddr("::1")}, {}})
	if err != nil {
		t.Fatalf("MapSlice: %v", err)
	}
	if len(got) != 2 || got[0].Gateway != "::1" || got[1].Gateway != "" {
		t.Errorf("MapSlice = %+v", got)
	}

	if _, err = MapSlice[stringConfig, netipConfig]([]stringConfig{{}, {Gateway: "bad"}}); err == nil {
		t.Error("MapSlice accepted an invalid address")
	}
}
