package validation

import (
	"regexp"
	"strings"

	"github.com/go-playground/validator/v10"
)

var (
	ethAddressRegex     = regexp.MustCompile(`^0x[a-fA-F0-9]{40}$`)
	tonAddressRegex     = regexp.MustCompile(`^(EQ|UQ)[a-zA-Z0-9_-]{46}$|^-?[0-9]+:[a-fA-F0-9]{64}$`)
	solanaAddressRegex  = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)
	bitcoinAddressRegex = regexp.MustCompile(`^(1|3)[a-km-zA-HJ-NP-Z1-9]{25,34}$|^bc1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{39,59}$`)
	tronAddressRegex    = regexp.MustCompile(`^T[a-zA-Z0-9]{33}$`)
)

// AddressValidator Validation rule for address
func AddressValidator(fl validator.FieldLevel) bool {
	// Get address field
	addr := fl.Field().String()

	// Get network field from parent struct
	networkField := fl.Parent().FieldByName("Network")
	if !networkField.IsValid() {
		return false
	}
	network := strings.ToLower(networkField.String())

	switch network {
	case "ethereum", "bsc":
		return ethAddressRegex.MatchString(addr)

	case "tron":
		return tronAddressRegex.MatchString(addr)

	case "bitcoin":
		return bitcoinAddressRegex.MatchString(addr)

	case "solana":
		return solanaAddressRegex.MatchString(addr)

	case "ton":
		return tonAddressRegex.MatchString(addr)

	default:
		return false
	}
}
