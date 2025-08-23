package validation

import (
	"sync"

	"wallets/internal/services/chains"

	"github.com/go-playground/validator/v10"
)

var (
	networks     map[string]struct{}
	networksOnce sync.Once
)

// NetworkValidator Validation rule for network
func NetworkValidator(fl validator.FieldLevel) bool {
	network := fl.Field().String()

	networksOnce.Do(func() {
		networks = make(map[string]struct{})
		for _, n := range chains.GetAvailableNetworks() {
			networks[n] = struct{}{}
		}
	})

	_, exists := networks[network]
	return exists
}
