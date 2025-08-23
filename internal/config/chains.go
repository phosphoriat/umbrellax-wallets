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
		"USDT":     "0xdAC17F958D2ee523a2206206994597C13D831ec7",
		"A8":       "0x3E5A19c91266aD8cE2477B91585d1856B84062dF",
		"APEX":     "0x52a8845df664d76c69d2eea607cd793565af42b8",
		"CMETH":    "0xE6829d9a7eE3040e1276Fa75293Bde931859e8fA",
		"CRV":      "0xd533a949740bb3306d119cc777fa900ba034cd52",
		"EIGEN":    "0xec53bF9167f50cDEB3Ae105f56099aaaB9061F83",
		"ENA":      "0x57e114B691Db790C35207b2e685D4A43181e6061",
		"GALA":     "0xd1d2eb1b1e90b638588728b4130137d262c87cae",
		"IRE5":     "0x3bd7d4F524D09F4e331577247A048D56e4b67a7F",
		"LINK":     "0x514910771af9ca656af840dff83e8264ecf986ca",
		"MOG":      "0xaaeE1A9723aaDB7afA2810263653A34bA2C21C7a",
		"NEIROCTO": "0x812Ba41e071C7b7fA4EBcFB62dF5F45f6fA853Ee",
		"ONDO":     "0xfAbA6f8e4a5E8Ab82F62fe7C39859FA577269BE3",
		"ONEINCH":  "0x111111111117dc0aa78b770fa6a738034120c302",
		"PEPE":     "0x6982508145454ce325ddbe47a25d4ec3d2311933",
		"SHIB":     "0x95ad61b0a150d79219dcf64e1e6cc01f0b64c4ce",
		"SPX":      "0xE0f63A424a4439cBE457D80E4f4b51aD25b2c56C",
		"STRK":     "0xCa14007Eff0dB1f8135f4C25B34De49AB0d42766",
		"USDC":     "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
		"XAUT":     "0x68749665FF8D2d112Fa859AA293F07A622782F38",
	},

	"bsc": map[string]string{
		"USDT":     "0x55d398326f99059fF775485246999027B3197955",
		"UMBRELLA": "0xf8ccF9F12f47DB271B1171be2E38B574f4050924",
		"BONE":     "0x2859e4544c4bb03966803b044a93563bd2d0dd4d",
		"SHIB":     "0x2859e4544c4bb03966803b044a93563bd2d0dd4d",
		"USDC":     "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d",
	},

	"ton": map[string]string{
		"USDT": "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs",
		"USDE": "EQAIb6KmdfdDR7CN1GBqVJuP25iCnLKCvBlJ07Evuu2dzP5f",
	},

	"solana": map[string]string{
		"USDT":     "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB",
		"AI16Z":    "HeLp6NuQkmYB4pYWo2zYs22mESHXPQYzXbB8n4V98jwC",
		"ALCH":     "HNg5PYJmtqcmzXrv6S9zP1CDKk5BgDuyFBxbvNApump",
		"BONK":     "DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263",
		"CHILLGUY": "Df6yfrKC8kZE3KNkrHERKzAetSxbrWeniQfyJY4Jpump",
		"GOAT":     "CzLSujWBLFsSjncfkh59rUFqvafWcY5tzedWJSuypump",
		"GRASS":    "Grass7B4RdKfBCjTKgSqnXkqjwiGvQyFbuSCUJr3XXjs",
		"PENGU":    "2zMMhcVQEXDtdE6vsFS7S7D5oUodfJHE8vd1gnBouauv",
		"PNUT":     "2qEHjDLDLbuBgRYvsxhc5D6uDWAivNFZGan56P1tpump",
		"POPCAT":   "7GCihgDB8fe6KNjn2MYtkzZcRjQy3t9GHdC8uHYmW2hr",
		"RENDER":   "rndrizKT3MK1iimdxRdWabcF7Zg7AR5T4nud4EkHBof",
		"TRUMP":    "6p6xgHyF7AeE6TZkSmFsko444wqoP15icUSqi2jfGiPN",
		"USDC":     "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		"W":        "85VBFQZC9TZkfaptBWjvUw7YbZjy52A6mjtPGjstQAmQ",
		"WIF":      "EKpQGSJtjMFqKZ9KQanSqYXRcF8fBopzLHYxdM65zcjm",
	},
}

// NetworkSettings Network settings
var NetworkSettings = map[string]any{
	"tron": map[string]any{
		"native_token": "TRX",
		"aml_token_id": "",
		"max_fee":      0.02,
	},
	"ethereum": map[string]any{
		"abi":          "[{\"inputs\":[{\"internalType\":\"address\",\"name\":\"owner\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"spender\",\"type\":\"address\"}],\"name\":\"allowance\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"spender\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"approve\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"}],\"name\":\"balanceOf\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"decimals\",\"outputs\":[{\"internalType\":\"uint8\",\"name\":\"\",\"type\":\"uint8\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"name\",\"outputs\":[{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"symbol\",\"outputs\":[{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"totalSupply\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"transfer\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"transferFrom\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
		"native_token": "ETH",
		"aml_token_id": "",
		"max_fee":      0.1,
	},
	"bsc": map[string]any{
		"native_token": "BNB",
		"aml_token_id": "",
		"max_fee":      0.05,
	},
	"ton": map[string]any{
		"native_token": "TON",
		"aml_token_id": "",
		"max_fee":      0.01,
	},
	"bitcoin": map[string]any{
		"native_token": "BTC",
		"aml_token_id": "",
		"max_fee":      0.0005,
	},
	"solana": map[string]any{
		"native_token": "SOL",
		"aml_token_id": "",
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
			return zero, fmt.Errorf("value at path %s has type %T, expected %s",
				path, value, reflect.TypeOf(zero).String())
		}
	} else {
		return zero, fmt.Errorf("value at path %s does not exist", path)
	}
}
