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
		for _, tokens := range config.ContractsAddresses {
			for token := range tokens {
				currencies[token] = struct{}{}
			}
		}
	})

	_, exists := currencies[currency]
	return exists
}
