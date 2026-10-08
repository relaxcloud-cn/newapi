package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeMacLimits(t *testing.T) {
	t.Run("enabled token requires whitelist", func(t *testing.T) {
		token := Token{MacCheckEnabled: true}

		require.Error(t, token.NormalizeMacLimits())
		require.NotNil(t, token.AllowMacs)
		assert.Empty(t, *token.AllowMacs)
	})

	t.Run("normalizes and deduplicates addresses", func(t *testing.T) {
		allowMacs := "94-B6-09-F6-4F-41\n94:b6:09:f6:4f:41\n00:11:22:33:44:55"
		token := Token{MacCheckEnabled: true, AllowMacs: &allowMacs}

		require.NoError(t, token.NormalizeMacLimits())
		require.NotNil(t, token.AllowMacs)
		assert.Equal(t, "94:b6:09:f6:4f:41\n00:11:22:33:44:55", *token.AllowMacs)
	})

	t.Run("rejects invalid address", func(t *testing.T) {
		allowMacs := "not-a-mac"
		token := Token{AllowMacs: &allowMacs}

		require.Error(t, token.NormalizeMacLimits())
	})

	t.Run("disabled token accepts empty whitelist", func(t *testing.T) {
		token := Token{}

		require.NoError(t, token.NormalizeMacLimits())
	})
}
