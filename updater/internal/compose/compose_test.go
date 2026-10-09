package compose

import "testing"

func TestComposeLabels(t *testing.T) {
	labels := map[string]string{
		ProjectLabelKey: " app ",
		ServiceLabelKey: "web",
	}
	if got := ProjectLabel(labels); got != "app" {
		t.Fatalf("ProjectLabel() = %q, want app", got)
	}
	if got := ServiceLabel(labels); got != "web" {
		t.Fatalf("ServiceLabel() = %q, want web", got)
	}
}
