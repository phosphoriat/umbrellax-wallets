package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port            string
	NotificatorPort string
	DbHost          string
	DbPort          string
	DbDatabase      string
	DbUser          string
	DbPassword      string
	RedisHost       string
	RedisPort       string
	RedisDatabase   string
	NatsHost        string
	NatsPort        string
	NatsUser        string
	NatsPassword    string
	NatsQueue       string
	VaultUrl        string
	VaultToken      string
	AmlApiUrl       string
	AmlApiKey       string
	AmlApiSecret    string
	AmlCheckCost    string
	TronRpcUrl      string
	EthRpcUrl       string
	BscRpcUrl       string
	TonRpcUrl       string
	BtcRpcUrl       string
	SolRpcUrl       string
	TatumWebhook    string
}

func Load() *Config {
	godotenv.Load()

	return &Config{
		Port:            getEnv("APP_PORT", ""),
		NotificatorPort: getEnv("NOTIFICATOR_PORT", ""),
		DbHost:          getEnv("DB_HOST", "localhost"),
		DbPort:          getEnv("DB_PORT", "5432"),
		DbDatabase:      getEnv("DB_DATABASE", ""),
		DbUser:          getEnv("DB_USER", ""),
		DbPassword:      getEnv("DB_PASSWORD", ""),
		RedisHost:       getEnv("REDIS_HOST", "localhost"),
		RedisPort:       getEnv("REDIS_PORT", "6379"),
		RedisDatabase:   getEnv("REDIS_DATABASE", ""),
		NatsHost:        getEnv("NATS_HOST", "localhost"),
		NatsPort:        getEnv("NATS_PORT", "4222"),
		NatsUser:        getEnv("NATS_USER", ""),
		NatsPassword:    getEnv("NATS_PASSWORD", ""),
		NatsQueue:       getEnv("NATS_QUEUE", ""),
		VaultUrl:        getEnv("VAULT_URL", ""),
		VaultToken:      getEnv("VAULT_TOKEN", ""),
		AmlApiUrl:       getEnv("AML_API_URL", ""),
		AmlApiKey:       getEnv("AML_API_KEY", ""),
		AmlApiSecret:    getEnv("AML_API_SECRET", ""),
		AmlCheckCost:    getEnv("AML_CHECK_COST", "0"),
		TronRpcUrl:      getEnv("TRON_RPC_URL", ""),
		EthRpcUrl:       getEnv("ETH_RPC_URL", ""),
		BscRpcUrl:       getEnv("BSC_RPC_URL", ""),
		TonRpcUrl:       getEnv("TON_RPC_URL", ""),
		BtcRpcUrl:       getEnv("BTC_RPC_URL", ""),
		SolRpcUrl:       getEnv("SOL_RPC_URL", ""),
		TatumWebhook:    getEnv("TATUM_WEBHOOK", ""),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
