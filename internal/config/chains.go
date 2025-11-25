package config

import (
	"fmt"
	"reflect"

	"wallets/internal/utils"
)

type ChainSettingValue interface {
	~int | ~int64 | ~float64 | ~string
}

// ContractsAddresses Blockchain smart contract addresses list for every token
var ContractsAddresses = map[string]any{
	"tron": map[string]string{
		"USDT": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
	},

	"ethereum": map[string]string{
		"USDT": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
		"USDC": "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
	},

	"bsc": map[string]string{
		"USDT":     "0x55d398326f99059fF775485246999027B3197955",
		"UMBRELLA": "0xf8ccF9F12f47DB271B1171be2E38B574f4050924",
		"USDC":     "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d",
	},

	"ton": map[string]string{
		"USDT": "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs",
	},

	"solana": map[string]string{
		"USDT": "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB",
		"USDC": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
	},
}

// NetworkSettings Network settings
var NetworkSettings = map[string]any{
	"tron": map[string]any{
		"native_token": "TRX",
		"max_fee":      0.02,
	},
	"ethereum": map[string]any{
		"abi":          "[{\"inputs\":[{\"internalType\":\"address\",\"name\":\"owner\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"spender\",\"type\":\"address\"}],\"name\":\"allowance\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"spender\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"approve\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"}],\"name\":\"balanceOf\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"decimals\",\"outputs\":[{\"internalType\":\"uint8\",\"name\":\"\",\"type\":\"uint8\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"name\",\"outputs\":[{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"symbol\",\"outputs\":[{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"totalSupply\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"transfer\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"transferFrom\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
		"native_token": "ETH",
		"max_fee":      0.1,
	},
	"bsc": map[string]any{
		"native_token": "BNB",
		"max_fee":      0.05,
	},
	"ton": map[string]any{
		"native_token": "TON",
		"max_fee":      0.01,
	},
	"bitcoin": map[string]any{
		"native_token": "BTC",
		"max_fee":      0.0005,
	},
	"solana": map[string]any{
		"native_token": "SOL",
		"max_fee":      0.03,
	},
}

// TokenSettings Token settings for every network
var TokenSettings = map[string]any{
	"tron": map[string]any{
		"TRX": map[string]any{
			"decimals":         6,
			"estimated_energy": 0,
			"min_merge_amount": 100.0,
		},
		"USDT": map[string]any{
			"estimated_energy": 85000,
			"min_merge_amount": 20.0,
		},
	},
	"ethereum": map[string]any{
		"ETH": map[string]any{
			"decimals":         18,
			"min_merge_amount": 0.01,
		},
		"USDT": map[string]any{
			"min_merge_amount": 10.0,
		},
	},
	"bsc": map[string]any{
		"BNB": map[string]any{
			"decimals":         18,
			"min_merge_amount": 0.1,
		},
		"USDT": map[string]any{
			"min_merge_amount": 15.0,
		},
		"UMBRELLA": map[string]any{
			"min_merge_amount": 7.5,
		},
	},
	"ton": map[string]any{
		"TON": map[string]any{
			"decimals":         9,
			"min_merge_amount": 0.5,
		},
		"USDT": map[string]any{
			"min_merge_amount": 5.0,
		},
	},
	"bitcoin": map[string]any{
		"BTC": map[string]any{
			"decimals":         8,
			"min_merge_amount": 0.001,
		},
	},
	"solana": map[string]any{
		"SOL": map[string]any{
			"decimals":         9,
			"min_merge_amount": 0.1,
		},
	},
}

// GetChainSetting Get chain setting from config
// config: Config interface
// path: Path in dot notation
// Returns: Config value from given path
func GetChainSetting[T ChainSettingValue](config map[string]any, path string) (T, error) {
	var zero T

	if value, ok := utils.DataGet(config, path); ok {
		if result, ok := value.(T); ok {
			return result, nil
		} else {
			return zero, fmt.Errorf("value at path '%s' has type '%T', expected '%s'",
				path, value, reflect.TypeOf(zero).String())
		}
	} else {
		return zero, fmt.Errorf("value at path '%s' does not exist", path)
	}
}

// GetTokenByContractAddr Get token name by network and contract address
func GetTokenByContractAddr(network, contractAddr string) (string, error) {
	networks, ok := ContractsAddresses[network]
	if !ok {
		return "", fmt.Errorf("network '%s' not found", network)
	}

	for tokenName, addr := range networks.(map[string]string) {
		if addr == contractAddr {
			return tokenName, nil
		}
	}

	return "", fmt.Errorf("token not found for address '%s' in network '%s'", contractAddr, network)
}
