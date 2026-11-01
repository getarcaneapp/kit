package kit

import "testing"

func TestParseBool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		value      string
		want       bool
		recognized bool
	}{
		{name: "true", value: "true", want: true, recognized: true},
		{name: "one", value: "1", want: true, recognized: true},
		{name: "yes", value: "yes", want: true, recognized: true},
		{name: "on", value: "on", want: true, recognized: true},
		{name: "false", value: "false", recognized: true},
		{name: "zero", value: "0", recognized: true},
		{name: "no", value: "no", recognized: true},
		{name: "off", value: "off", recognized: true},
		{name: "case and whitespace", value: " \tYeS\n", want: true, recognized: true},
		{name: "false case and whitespace", value: " OFF ", recognized: true},
		{name: "empty", value: ""},
		{name: "unknown", value: "maybe"},
		{name: "partial match", value: "yesplease"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, recognized := ParseBool(tt.value)
			if got != tt.want || recognized != tt.recognized {
				t.Errorf("ParseBool(%q) = (%t, %t), want (%t, %t)", tt.value, got, recognized, tt.want, tt.recognized)
			}
		})
	}
}
