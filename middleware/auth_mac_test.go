package middleware

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenAllowsMac(t *testing.T) {
	allowedMacs := "94:b6:09:f6:4f:41\n00:11:22:33:44:55"
	invalidMacs := "not-a-mac"

	tests := []struct {
		name      string
		token     *model.Token
		clientMac string
		allowed   bool
		wantError bool
	}{
		{name: "disabled token skips validation", token: &model.Token{}, allowed: true},
		{name: "enabled token accepts listed MAC", token: &model.Token{MacCheckEnabled: true, AllowMacs: &allowedMacs}, clientMac: "94-B6-09-F6-4F-41", allowed: true},
		{name: "enabled token rejects missing header", token: &model.Token{MacCheckEnabled: true, AllowMacs: &allowedMacs}},
		{name: "enabled token rejects malformed header", token: &model.Token{MacCheckEnabled: true, AllowMacs: &allowedMacs}, clientMac: "invalid"},
		{name: "enabled token rejects unlisted MAC", token: &model.Token{MacCheckEnabled: true, AllowMacs: &allowedMacs}, clientMac: "aa:bb:cc:dd:ee:ff"},
		{name: "invalid configured MAC fails closed", token: &model.Token{MacCheckEnabled: true, AllowMacs: &invalidMacs}, clientMac: "94:b6:09:f6:4f:41", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allowed, err := tokenAllowsMac(test.token, test.clientMac)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.allowed, allowed)
		})
	}
}
