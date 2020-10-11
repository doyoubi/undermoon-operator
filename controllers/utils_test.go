package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnitCompression(t *testing.T) {
	assert := assert.New(t)

	origin := "testabc12345"
	compressed, err := compressString(origin)
	assert.NoError(err)
	assert.True(len(compressed) > 0)

	decompressed, err := decompressString(compressed)
	assert.NoError(err)
	assert.Equal(origin, decompressed)
}
