package buffer

import "testing"

func TestSize(t *testing.T) {
	tests := []struct {
		size *int
		name string
		want int
	}{
		{name: "unset", want: 8},
		{name: "zero is unbuffered", size: new(0), want: 0},
		{name: "positive", size: new(3), want: 3},
		{name: "negative uses fallback", size: new(-1), want: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Size(tt.size, 8); got != tt.want {
				t.Fatalf("Size() = %d, want %d", got, tt.want)
			}
		})
	}
}
