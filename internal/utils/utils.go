package utils

import (
	"strconv"
	"strings"
)

// DataGet Get value from map or array by path in dot notation
func DataGet(data any, path string) (any, bool) {
	if data == nil {
		return nil, false
	}
	if path == "" {
		return data, true
	}

	keys := strings.Split(path, ".")

	current := data
	for i, key := range keys {
		switch v := current.(type) {
		case map[string]any:
			if value, exists := v[key]; exists {
				current = value
			} else {
				return nil, false
			}

		case []any:
			if index, err := strconv.Atoi(key); err == nil {
				if index >= 0 && index < len(v) {
					current = v[index]
				} else {
					return nil, false
				}
			} else {
				return nil, false
			}

		default:
			if i == len(path)-1 {
				return current, true
			}
			return nil, false
		}
	}

	return current, true
}
