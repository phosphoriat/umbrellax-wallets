package validation

import (
	"strings"

	"github.com/go-playground/validator/v10"
)

// AddressValidator Validation rule for address
func AddressValidator(fl validator.FieldLevel) bool {
	query := fl.Parent().Interface().(struct {
		Network string
		Hash    string
		To      string
	})
	addr := query.To
	network := query.Network

	switch network {
	case "ethereum", "bsc":
		return strings.HasPrefix(addr, "0x") && len(addr) == 42

	case "tron":
		return strings.HasPrefix(addr, "T") && len(addr) == 34

	case "bitcoin":
		return strings.HasPrefix(addr, "1") ||
			strings.HasPrefix(addr, "3") ||
			strings.HasPrefix(addr, "bc1")

	case "solana":
		return len(addr) == 44

	case "ton":
		return strings.HasPrefix(addr, "UQ") && len(addr) > 30

	default:
		return false
	}
}
