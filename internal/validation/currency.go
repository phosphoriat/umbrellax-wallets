package validation

import (
	"sync"
	"wallets/internal/config"

	"github.com/go-playground/validator/v10"
)

var (
	currencies     map[string]struct{}
	currenciesOnce sync.Once
)

// CurrencyValidator Validation rule for currency
func CurrencyValidator(fl validator.FieldLevel) bool {
	currency := fl.Field().String()

	currenciesOnce.Do(func() {
		currencies = make(map[string]struct{})

		// Add contract tokens (USDT, etc.)
		for _, tokens := range config.ContractsAddresses {
			for token := range tokens {
				currencies[token] = struct{}{}
			}
		}

		// Add native tokens
		for _, settings := range config.NetworkSettings {
			if settings.NativeToken != "" {
				currencies[settings.NativeToken] = struct{}{}
			}
		}
	})

	_, exists := currencies[currency]
	return exists
}
