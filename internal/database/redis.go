package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"wallets/internal/config"

	"github.com/redis/go-redis/v9"
)

// Global Redis connection var
var redisConnection *redis.Client

// InitRedis Init connection to Redis
func InitRedis(cfg *config.Config) error {
	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDatabase,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pong, err := client.Ping(ctx).Result()
	if err != nil {
		return fmt.Errorf("failed to ping Redis: %w", err)
	}
	if pong != "PONG" {
		return fmt.Errorf("unexpected ping response from Redis: %s", pong)
	}

	// Assign connection to global var
	redisConnection = client
	log.Printf("Redis connection established: %s:%s (DB: %d)",
		cfg.RedisHost, cfg.RedisPort, cfg.RedisDatabase)

	return nil
}

// GetRedisConnection Return Redis connection instance
func GetRedisConnection() *redis.Client {
	return redisConnection
}

// CloseRedis Close Redis connection
func CloseRedis() error {
	if redisConnection != nil {
		return redisConnection.Close()
	}

	return nil
}

// SetValue Set value in Redis with expiration
func SetValue(key string, value interface{}, expiration time.Duration) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.Set(ctx, key, value, expiration).Err()
}

// GetValue Get value from Redis
func GetValue(key string) (string, error) {
	if redisConnection == nil {
		return "", fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.Get(ctx, key).Result()
}

// DeleteValue Delete value from Redis
func DeleteValue(key string) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.Del(ctx, key).Err()
}

// SetHash Set hash field value
func SetHash(key, field string, value interface{}) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.HSet(ctx, key, field, value).Err()
}

// GetHash Get hash field value
func GetHash(key, field string) (string, error) {
	if redisConnection == nil {
		return "", fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.HGet(ctx, key, field).Result()
}

// GetAllHash Get all hash fields and values
func GetAllHash(key string) (map[string]string, error) {
	if redisConnection == nil {
		return nil, fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.HGetAll(ctx, key).Result()
}

// PushToList Push value to list (left push)
func PushToList(key string, values ...interface{}) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.LPush(ctx, key, values...).Err()
}

// PopFromList Pop value from list (right pop)
func PopFromList(key string) (string, error) {
	if redisConnection == nil {
		return "", fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.RPop(ctx, key).Result()
}

// GetListLength Get list length
func GetListLength(key string) (int64, error) {
	if redisConnection == nil {
		return 0, fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.LLen(ctx, key).Result()
}

// AddToSet Add value to set
func AddToSet(key string, values ...interface{}) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.SAdd(ctx, key, values...).Err()
}

// IsInSet Check if value is in set
func IsInSet(key string, value interface{}) (bool, error) {
	if redisConnection == nil {
		return false, fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.SIsMember(ctx, key, value).Result()
}

// GetSetMembers Get all set members
func GetSetMembers(key string) ([]string, error) {
	if redisConnection == nil {
		return nil, fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.SMembers(ctx, key).Result()
}
