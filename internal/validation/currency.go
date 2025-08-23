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
		for _, v := range config.ContractsAddresses {
			if innerMap, ok := v.(map[string]string); ok {
				for key := range innerMap {
					currencies[key] = struct{}{}
				}
			}
		}
	})

	_, exists := currencies[currency]
	return exists
}
