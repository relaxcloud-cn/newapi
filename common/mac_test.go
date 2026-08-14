package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeMacAddress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		valid    bool
	}{
		{name: "colon separated", input: "94:b6:09:f6:4f:41", expected: "94:b6:09:f6:4f:41", valid: true},
		{name: "hyphen separated uppercase", input: "94-B6-09-F6-4F-41", expected: "94:b6:09:f6:4f:41", valid: true},
		{name: "surrounding whitespace", input: " 94:b6:09:f6:4f:41 ", expected: "94:b6:09:f6:4f:41", valid: true},
		{name: "missing octet", input: "94:b6:09:f6:4f", valid: false},
		{name: "eui64 is not a client MAC", input: "94:b6:09:f6:4f:41:00:01", valid: false},
		{name: "empty", input: "", valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := NormalizeMacAddress(test.input)
			if !test.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expected, actual)
		})
	}
}
