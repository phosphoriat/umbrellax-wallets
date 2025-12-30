package config

import (
	"cmp"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv  string
	AppUrl  string
	AppPort string
	// PostgreSQL
	PgSqlHost     string
	PgSqlPort     string
	PgSqlName     string
	PgSqlUser     string
	PgSqlPassword string
	PgSqlSslMode  string
	// ClickHouse
	ClickHouseHost     string
	ClickHousePort     string
	ClickHouseName     string
	ClickHouseUser     string
	ClickHousePassword string
	// Redis
	RedisHost                string
	RedisPort                string
	RedisDatabase            int
	RedisPassword            string
	RedisWithdrawalsKey      string
	RedisRefillsKey          string
	RedisBybitReqsKey        string
	RedisBybitWithdrawalsKey string
	// Vault
	VaultScheme   string
	VaultHost     string
	VaultPort     string
	VaultRoleId   string
	VaultSecretId string
	// NATS
	NatsHost     string
	NatsPort     string
	NatsUser     string
	NatsPassword string
	NatsQueue    string
	// Bybit
	BybitWsUrl     string
	BybitApiUrl    string
	BybitApiKey    string
	BybitApiSecret string
	// AML
	AmlApiUrl    string
	AmlApiKey    string
	AmlApiSecret string
	AmlCheckCost float64
	AmlThreshold float64
	// Rates
	RatesApiUrl string
	// QuickNode
	QuickNodeApiKey string
	QuickNodeRps    int
	// Chains
	TronRpcUrl   string
	EthRpcUrl    string
	BscRpcUrl    string
	SolRpcUrl    string
	TonConfigUrl string
	TonHttpUrl   string
	BtcNodeUrl   string
	BtcNodeUser  string
	BtcNodePass  string
	// Notifications
	TelegramGroupId   int64
	TelegramBotToken  string
	MessengerApiUrl   string
	MessengerBotId    string
	MessengerBotToken string
}

// Load config from env and return Config struct
func Load() (*Config, error) {
	// Read env file and load into ENV for this process
	err := godotenv.Load()
	if err != nil {
		return nil, err
	}

	return &Config{
		AppEnv:  getEnv("APP_ENV", ""),
		AppUrl:  getEnv("APP_URL", ""),
		AppPort: getEnv("APP_PORT", ""),
		// PostgreSQL
		PgSqlHost:     getEnv("PGSQL_HOST", "localhost"),
		PgSqlPort:     getEnv("PGSQL_PORT", "5432"),
		PgSqlName:     getEnv("PGSQL_DATABASE", ""),
		PgSqlUser:     getEnv("PGSQL_USERNAME", ""),
		PgSqlPassword: getEnv("PGSQL_PASSWORD", ""),
		PgSqlSslMode:  getEnv("PGSQL_SSL_MODE", "required"),
		// ClickHouse
		ClickHouseHost:     getEnv("CH_HOST", "localhost"),
		ClickHousePort:     getEnv("CH_PORT", "8123"),
		ClickHouseName:     getEnv("CH_DATABASE", ""),
		ClickHouseUser:     getEnv("CH_USERNAME", ""),
		ClickHousePassword: getEnv("CH_PASSWORD", ""),
		// Redis
		RedisHost:                getEnv("REDIS_HOST", "localhost"),
		RedisPort:                getEnv("REDIS_PORT", "6379"),
		RedisDatabase:            getEnvInt("REDIS_DB", 2),
		RedisPassword:            getEnv("REDIS_PASSWORD", ""),
		RedisBybitReqsKey:        getEnv("REDIS_BYBIT_REQUESTS_KEY", ""),
		RedisBybitWithdrawalsKey: getEnv("REDIS_BYBIT_WITHDRAWALS_KEY", ""),
		RedisWithdrawalsKey:      getEnv("REDIS_WITHDRAWALS_KEY", ""),
		RedisRefillsKey:          getEnv("REDIS_REFILLS_KEY", ""),
		// Vault
		VaultScheme:   getEnv("VAULT_SCHEME", "http"),
		VaultHost:     getEnv("VAULT_HOST", "localhost"),
		VaultPort:     getEnv("VAULT_PORT", ""),
		VaultRoleId:   getEnv("VAULT_ROLE_ID", ""),
		VaultSecretId: getEnv("VAULT_SECRET_ID", ""),
		// NATS
		NatsHost:     getEnv("NATS_HOST", "localhost"),
		NatsPort:     getEnv("NATS_PORT", "4222"),
		NatsUser:     getEnv("NATS_USER", ""),
		NatsPassword: getEnv("NATS_PASSWORD", ""),
		NatsQueue:    getEnv("NATS_QUEUE", ""),
		// Bybit
		BybitWsUrl:     getEnv("BYBIT_WS_URL", ""),
		BybitApiUrl:    getEnv("BYBIT_API_URL", ""),
		BybitApiKey:    getEnv("BYBIT_API_KEY", ""),
		BybitApiSecret: getEnv("BYBIT_API_SECRET", ""),
		// AML
		AmlApiUrl:    getEnv("AML_API_URL", ""),
		AmlApiKey:    getEnv("AML_API_KEY", ""),
		AmlApiSecret: getEnv("AML_API_SECRET", ""),
		AmlCheckCost: getEnvFloat("AML_CHECK_COST", 0),
		AmlThreshold: getEnvFloat("AML_THRESHOLD", 0),
		// Rates
		RatesApiUrl: getEnv("RATES_API_URL", ""),
		// QuickNode
		QuickNodeApiKey: getEnv("QUICKNODE_API_KEY", ""),
		QuickNodeRps:    getEnvInt("QUICKNODE_RPS", 15),
		// Chains
		TronRpcUrl:   getEnv("TRON_RPC_URL", ""),
		EthRpcUrl:    getEnv("ETH_RPC_URL", ""),
		BscRpcUrl:    getEnv("BSC_RPC_URL", ""),
		SolRpcUrl:    getEnv("SOL_RPC_URL", ""),
		TonConfigUrl: getEnv("TON_CONFIG_URL", ""),
		TonHttpUrl:   getEnv("TON_HTTP_URL", ""),
		BtcNodeUrl:   getEnv("BTC_NODE_URL", ""),
		BtcNodeUser:  getEnv("BTC_NODE_USER", ""),
		BtcNodePass:  getEnv("BTC_NODE_PASS", ""),
		// Notifications
		TelegramGroupId:   int64(getEnvInt("TELEGRAM_GROUP_ID", 0)),
		TelegramBotToken:  getEnv("TELEGRAM_BOT_TOKEN", ""),
		MessengerApiUrl:   getEnv("MESSENGER_API_URL", ""),
		MessengerBotId:    getEnv("MESSENGER_BOT_ID", ""),
		MessengerBotToken: getEnv("MESSENGER_BOT_TOKEN", ""),
	}, nil
}

// getEnv Get string value of env variable by key
func getEnv(key, defaultValue string) string {
	return cmp.Or(os.Getenv(key), defaultValue)
}

// getEnvInt Get value of env variable parsed to int
func getEnvInt(key string, defaultValue int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return defaultValue
}

// getEnvFloat Get value of env variable parsed to float
func getEnvFloat(key string, defaultValue float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return defaultValue
}
