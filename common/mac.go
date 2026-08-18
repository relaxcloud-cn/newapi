package common

import (
	"fmt"
	"net"
	"strings"
)

// NormalizeMacAddress validates an EUI-48 address and returns its canonical form.
func NormalizeMacAddress(value string) (string, error) {
	hardwareAddress, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil || len(hardwareAddress) != 6 {
		return "", fmt.Errorf("invalid MAC address: %q", value)
	}
	return strings.ToLower(hardwareAddress.String()), nil
}
