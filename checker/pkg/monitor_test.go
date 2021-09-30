package pkg

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRingBuf(t *testing.T) {
	assert := assert.New(t)

	r := newRingBuf(4)

	assert.Equal(0, r.len())
	assert.Equal(0, len(r.data()))

	r.add("1")
	assert.Equal(1, r.len())
	assert.Equal([]string{"1"}, r.data())

	r.add("2")
	assert.Equal(2, r.len())
	assert.Equal([]string{"1", "2"}, r.data())

	r.add("3")
	assert.Equal(3, r.len())
	assert.Equal([]string{"1", "2", "3"}, r.data())

	r.add("4")
	assert.Equal(3, r.len())
	assert.Equal([]string{"2", "3", "4"}, r.data())

	r.add("5")
	assert.Equal(3, r.len())
	assert.Equal([]string{"3", "4", "5"}, r.data())

	r.add("6")
	assert.Equal(3, r.len())
	assert.Equal([]string{"4", "5", "6"}, r.data())

	r.add("7")
	assert.Equal(3, r.len())
	assert.Equal([]string{"5", "6", "7"}, r.data())
}
