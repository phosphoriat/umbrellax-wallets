package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	AppUrl          string
	AppPort         string
	NotificatorPort string
	// PostgreSQL
	DbHost     string
	DbPort     string
	DbName     string
	DbUser     string
	DbPassword string
	DbSslMode  string
	// ClickHouse
	ClickhouseHost     string
	ClickhousePort     string
	ClickhouseName     string
	ClickhouseUser     string
	ClickhousePassword string
	// Redis
	RedisHost           string
	RedisPort           string
	RedisDatabase       int
	RedisPassword       string
	RedisWithdrawalsKey string
	RedisRefillsKey     string
	RedisBybitReqsKey   string
	// Vault
	VaultHost     string
	VaultPort     string
	VaultRoleId   string
	VaultSecretId string
	VaultScheme   string
	VaultToken    string
	// NATS
	NatsHost     string
	NatsPort     string
	NatsUser     string
	NatsPassword string
	NatsQueue    string
	// Bybit
	BybitApiUrl    string
	BybitWsUrl     string
	BybitApiKey    string
	BybitApiSecret string
	// AML
	AmlApiUrl    string
	AmlApiKey    string
	AmlApiSecret string
	AmlCheckCost string
	// RPC urls
	TronRpcUrl string
	EthRpcUrl  string
	BscRrcUrl  string
	BtcRpcUrl  string
	SolRpcUrl  string
	// Tatum
	TatumApiKey  string
	TatumWebhook string
}

// Load config from env and return Config struct
func Load() *Config {
	// Read env file and load into ENV for this process
	err := godotenv.Load()
	if err != nil {
		return nil
	}

	return &Config{
		AppUrl:          getEnv("APP_URL", ""),
		AppPort:         getEnv("APP_PORT", ""),
		NotificatorPort: getEnv("NOTIFICATOR_PORT", ""),
		// PostgreSQL
		DbHost:     getEnv("DB_HOST", "localhost"),
		DbPort:     getEnv("DB_PORT", "5432"),
		DbName:     getEnv("DB_DATABASE", ""),
		DbUser:     getEnv("DB_USER", ""),
		DbPassword: getEnv("DB_PASSWORD", ""),
		DbSslMode:  getEnv("DB_SSL_MODE", "required"),
		// ClickHouse
		ClickhouseHost:     getEnv("CLICKHOUSE_HOST", "localhost"),
		ClickhousePort:     getEnv("CLICKHOUSE_PORT", "8123"),
		ClickhouseName:     getEnv("CLICKHOUSE_DATABASE", ""),
		ClickhouseUser:     getEnv("CLICKHOUSE_USER", ""),
		ClickhousePassword: getEnv("CLICKHOUSE_PASSWORD", ""),
		// Redis
		RedisHost:           getEnv("REDIS_HOST", "localhost"),
		RedisPort:           getEnv("REDIS_PORT", "6379"),
		RedisDatabase:       getEnvAsInt("REDIS_DATABASE", 3),
		RedisPassword:       getEnv("REDIS_PASSWORD", ""),
		RedisBybitReqsKey:   getEnv("REDIS_BYBIT_REQS_KEY", ""),
		RedisWithdrawalsKey: getEnv("REDIS_WITHDRAWALS_KEY", ""),
		RedisRefillsKey:     getEnv("REDIS_REFILLS_KEY", ""),
		// Vault
		VaultHost:     getEnv("VAULT_HOST", "localhost"),
		VaultPort:     getEnv("VAULT_PORT", ""),
		VaultRoleId:   getEnv("VAULT_ROLE_ID", ""),
		VaultSecretId: getEnv("VAULT_SECRET_ID", ""),
		VaultScheme:   getEnv("VAULT_SCHEME", "http"),
		VaultToken:    getEnv("VAULT_SCHEME", ""),
		// NATS
		NatsHost:     getEnv("NATS_HOST", "localhost"),
		NatsPort:     getEnv("NATS_PORT", "4222"),
		NatsUser:     getEnv("NATS_USER", ""),
		NatsPassword: getEnv("NATS_PASSWORD", ""),
		NatsQueue:    getEnv("NATS_QUEUE", ""),
		// Bybit
		BybitApiUrl:    getEnv("BYBIT_API_URL", ""),
		BybitWsUrl:     getEnv("BYBIT_WS_URL", ""),
		BybitApiKey:    getEnv("BYBIT_API_KEY", ""),
		BybitApiSecret: getEnv("BYBIT_API_SECRET", ""),
		// AML
		AmlApiUrl:    getEnv("AML_API_URL", ""),
		AmlApiKey:    getEnv("AML_API_KEY", ""),
		AmlApiSecret: getEnv("AML_API_SECRET", ""),
		AmlCheckCost: getEnv("AML_CHECK_COST", "0"),
		// RPC urls
		TronRpcUrl: getEnv("TRON_RPC_URL", ""),
		EthRpcUrl:  getEnv("ETH_RPC_URL", ""),
		BscRrcUrl:  getEnv("BSC_RRC_URL", ""),
		BtcRpcUrl:  getEnv("BTC_RPC_URL", ""),
		SolRpcUrl:  getEnv("SOL_RPC_URL", ""),
		// Tatum
		TatumApiKey:  getEnv("TATUM_API_KEY", ""),
		TatumWebhook: getEnv("TATUM_WEBHOOK", ""),
	}
}

// getEnv Get string value of the env variable named by the key
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return defaultValue
}

// getEnvAsInt Get value of the env variable parsed to int
func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		} else {
			log.Printf("Invalid number value for %s: %s", key, value)
		}
	}

	return defaultValue
}
