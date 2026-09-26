package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelectDepotArchInternal(t *testing.T) {
	assert.Equal(t, "arm64", selectDepotArchInternal([]string{"linux/arm64/v8"}))
	assert.Equal(t, "amd64", selectDepotArchInternal([]string{"linux/x86_64"}))
	assert.Equal(t, "arm64", selectDepotArchInternal([]string{"not a platform", "linux/aarch64"}))
}
