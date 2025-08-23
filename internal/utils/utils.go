package utils

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// ApplyDecimals Convert float64 to *big.Int with decimals
func ApplyDecimals(amount float64, decimals int64) *big.Int {
	amountStr := strconv.FormatFloat(amount, 'f', -1, 64)
	f, _ := new(big.Float).SetString(amountStr)

	factor := new(big.Float).SetInt(
		new(big.Int).Exp(big.NewInt(10), big.NewInt(decimals), nil),
	)

	f.Mul(f, factor)
	result := new(big.Int)
	f.Int(result)

	return result
}

// RemoveDecimals Convert *big.Int to float64 with decimals
func RemoveDecimals(amount *big.Int, decimals int64) float64 {
	factor := new(big.Float).SetInt(
		new(big.Int).Exp(big.NewInt(10), big.NewInt(decimals), nil),
	)

	result, _ := new(big.Float).Quo(new(big.Float).SetInt(amount), factor).Float64()
	return result
}

// DataGet Get value from map or array by path in dot notation
func DataGet(data any, path string) (any, bool) {
	if data == nil {
		return nil, false
	}
	if path == "" {
		return data, true
	}

	keys := strings.FieldsFunc(path, func(r rune) bool {
		return r == '.'
	})
	current := data

	for i, key := range keys {
		switch v := current.(type) {
		case map[string]any:
			if value, exists := v[key]; exists {
				current = value
			} else {
				return nil, false
			}

		case map[any]any:
			found := false
			for k, val := range v {
				if fmt.Sprint(k) == key {
					current = val
					found = true
					break
				}
			}
			if !found {
				return nil, false
			}

		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(v) {
				return nil, false
			}
			current = v[index]

		default:
			if i == len(keys)-1 {
				return current, true
			}
			return nil, false
		}
	}

	return current, true
}

// DataSet Set value of map or array by path in dot notation
func DataSet(data any, path string, value any) (any, bool) {
	if path == "" {
		return value, true
	}

	keys := strings.Split(path, ".")
	current := data

	var parent any
	var parentKey string
	var parentIndex int
	var parentIsSlice bool

	for i, key := range keys {
		last := i == len(keys)-1

		switch v := current.(type) {
		case map[string]any:
			if last {
				v[key] = value
				return data, true
			}

			if _, exists := v[key]; !exists {
				if i+1 < len(keys) {
					if _, err := strconv.Atoi(keys[i+1]); err == nil {
						v[key] = []any{}
					} else {
						v[key] = map[string]any{}
					}
				} else {
					v[key] = map[string]any{}
				}
			}

			parent, parentKey, parentIsSlice = v, key, false
			current = v[key]

		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 {
				return nil, false
			}

			if index >= len(v) {
				newSlice := make([]any, index+1)
				copy(newSlice, v)
				v = newSlice

				if parentIsSlice {
					parent.([]any)[parentIndex] = v
				} else {
					parent.(map[string]any)[parentKey] = v
				}
			}

			if last {
				v[index] = value
				return data, true
			}

			if v[index] == nil {
				if i+1 < len(keys) {
					if _, err := strconv.Atoi(keys[i+1]); err == nil {
						v[index] = []any{}
					} else {
						v[index] = map[string]any{}
					}
				} else {
					v[index] = map[string]any{}
				}
			}

			parent, parentIndex, parentIsSlice = v, index, true
			current = v[index]

		default:
			return nil, false
		}
	}

	return data, true
}
