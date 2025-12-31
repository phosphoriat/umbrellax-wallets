package config

const ERC20ABI = `[{"inputs":[{"internalType":"address","name":"owner","type":"address"},{"internalType":"address","name":"spender","type":"address"}],"name":"allowance","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"decimals","outputs":[{"internalType":"uint8","name":"","type":"uint8"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"name","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"symbol","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"totalSupply","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"recipient","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"sender","type":"address"},{"internalType":"address","name":"recipient","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"transferFrom","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`

type (
	NetworkConfig struct {
		NativeToken  string
		BybitAddress string
		FeeBuffer    float64
		GasLimit     int
		BlockTime    int
		ABI          string
		ForwardFee   string
		MessageFee   string
		FallbackFee  float64
	}

	TokenConfig struct {
		Decimals        int
		EstimatedEnergy int
	}
)

// ContractsAddresses Blockchain smart contract addresses list for every token
var ContractsAddresses = map[string]map[string]string{
	"tron": {
		"USDT": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
	},
	"ethereum": {
		"USDT": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
	},
	"bsc": {
		"USDT": "0x55d398326f99059fF775485246999027B3197955",
	},
	"ton": {
		"USDT":     "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs",
		"UMBRELLA": "EQBpml9LfJoC2qGZXCGDCSA0BCNvTsoONJV-bGhW1nHkWWTR",
	},
	"solana": {
		"USDT": "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB",
	},
}

// NetworkSettings Network settings
var NetworkSettings = map[string]NetworkConfig{
	"tron": {
		NativeToken:  "TRX",
		BybitAddress: "TVWE87Z75Hm6yeAFcYrZjkuanNKpq6eM3k",
		FeeBuffer:    0.05,
		BlockTime:    3,
	},
	"ethereum": {
		ABI:          ERC20ABI,
		NativeToken:  "ETH",
		BybitAddress: "0x0182f8cff05f1554ccbc14cbe06c5a8973c85c31",
		FeeBuffer:    0.1,
		GasLimit:     65000,
		BlockTime:    12,
	},
	"bsc": {
		ABI:          ERC20ABI,
		NativeToken:  "BNB",
		BybitAddress: "0x0182f8cff05f1554ccbc14cbe06c5a8973c85c31",
		FeeBuffer:    0.05,
		GasLimit:     65000,
		BlockTime:    1,
	},
	"ton": {
		NativeToken:  "TON",
		BybitAddress: "UQAjUG-ewikNc-uEtZLCM0DbxkV2SgCh27Wyf6-JzNexlrvG",
		FeeBuffer:    0.05,
		ForwardFee:   "0.02",
		MessageFee:   "0.01",
		FallbackFee:  0.05,
		BlockTime:    5,
	},
	"bitcoin": {
		NativeToken:  "BTC",
		BybitAddress: "1E61LTQx5CRAwptFCJmAdzmbiXNCn1Ttat",
		FeeBuffer:    0.05,
		BlockTime:    600,
	},
	"solana": {
		NativeToken:  "SOL",
		BybitAddress: "4Ctt4Ud27BMP7aDfTBHrm1MD2Q6gGu7gBE3JJmPuQfBZ",
		FeeBuffer:    0.05,
		BlockTime:    1,
	},
}

// TokenSettings Token settings for every network
// For TON every token must have decimals, for other networks only native token
var TokenSettings = map[string]map[string]TokenConfig{
	"tron": {
		"TRX": {
			Decimals:        6,
			EstimatedEnergy: 0,
		},
		"USDT": {
			EstimatedEnergy: 85000,
		},
	},
	"ethereum": {
		"ETH": {
			Decimals: 18,
		},
	},
	"bsc": {
		"BNB": {
			Decimals: 18,
		},
	},
	"ton": {
		"TON": {
			Decimals: 9,
		},
		"USDT": {
			Decimals: 6,
		},
		"UMBRELLA": {
			Decimals: 15,
		},
	},
	"bitcoin": {
		"BTC": {
			Decimals: 8,
		},
	},
	"solana": {
		"SOL": {
			Decimals: 9,
		},
	},
}

// GetNetworkConfig Get network config
// network: Network code
// Returns: NetworkConfig struct
func GetNetworkConfig(network string) (NetworkConfig, bool) {
	cfg, ok := NetworkSettings[network]
	return cfg, ok
}

// GetTokenConfig Get token config
// network: Network code
// token: Token ticker
// Returns: TokenConfig struct
func GetTokenConfig(network, token string) (TokenConfig, bool) {
	tokens, ok := TokenSettings[network]
	if !ok {
		return TokenConfig{}, false
	}

	cfg, ok := tokens[token]
	return cfg, ok
}

// GetContractAddress Get contract address for token
// network: Network code
// token: Token ticker
// Returns: Token contract address
func GetContractAddress(network, token string) (string, bool) {
	tokens, ok := ContractsAddresses[network]
	if !ok {
		return "", false
	}

	addr, ok := tokens[token]
	return addr, ok
}

// GetTokenByContractAddr Get token name by network and contract address
// network: Token network
// contractAddr: Token contract address
// Returns: Token ticker
func GetTokenByContractAddr(network, contractAddr string) (string, bool) {
	tokens, ok := ContractsAddresses[network]
	if !ok {
		return "", false
	}

	for token, addr := range tokens {
		if addr == contractAddr {
			return token, true
		}
	}
	return "", false
}
