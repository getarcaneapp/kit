package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelectDepotArch(t *testing.T) {
	assert.Equal(t, "arm64", selectDepotArch([]string{"linux/arm64/v8"}))
	assert.Equal(t, "amd64", selectDepotArch([]string{"linux/x86_64"}))
	assert.Equal(t, "arm64", selectDepotArch([]string{"not a platform", "linux/aarch64"}))
}
