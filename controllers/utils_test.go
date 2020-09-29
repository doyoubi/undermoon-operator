package controllers

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompression(t *testing.T) {
	assert := assert.New(t)

	origin := "testabc12345"
	compressed, err := compressString(origin)
	assert.NoError(err)
	assert.True(len(compressed) > 0)

	fmt.Printf("@@@%s@@@\n", compressed)

	decompressed, err := decompressString(compressed)
	assert.NoError(err)
	assert.Equal(origin, decompressed)
}
