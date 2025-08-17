package redis

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

// Init connection to Redis
func Init(cfg *config.Config) error {
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

// Close Redis connection
func Close() error {
	if redisConnection != nil {
		return redisConnection.Close()
	}
	return nil
}

// Set string value in Redis with expiration
func Set(key string, value interface{}, expiration time.Duration) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.Set(ctx, key, value, expiration).Err()
}

// Get string value from Redis
func Get(key string) (string, error) {
	if redisConnection == nil {
		return "", fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.Get(ctx, key).Result()
}

// Del string value from Redis
func Del(key string) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.Del(ctx, key).Err()
}

// HSet Set hash field value
func HSet(key, field string, value interface{}) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.HSet(ctx, key, field, value).Err()
}

// HGet Get hash field value
func HGet(key, field string) (string, error) {
	if redisConnection == nil {
		return "", fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.HGet(ctx, key, field).Result()
}

// HGetAll Get all hash fields and values
func HGetAll(key string) (map[string]string, error) {
	if redisConnection == nil {
		return nil, fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.HGetAll(ctx, key).Result()
}

// LPush Push value to list (left push)
func LPush(key string, values ...interface{}) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.LPush(ctx, key, values...).Err()
}

// RPop Pop value from list (right pop)
func RPop(key string) (string, error) {
	if redisConnection == nil {
		return "", fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.RPop(ctx, key).Result()
}

// LLen Get list length
func LLen(key string) (int64, error) {
	if redisConnection == nil {
		return 0, fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.LLen(ctx, key).Result()
}

// SAdd Add value to set
func SAdd(key string, values ...interface{}) error {
	if redisConnection == nil {
		return fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.SAdd(ctx, key, values...).Err()
}

// SIsMember Check if value is in set
func SIsMember(key string, value interface{}) (bool, error) {
	if redisConnection == nil {
		return false, fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.SIsMember(ctx, key, value).Result()
}

// SMembers Get all set members
func SMembers(key string) ([]string, error) {
	if redisConnection == nil {
		return nil, fmt.Errorf("redis connection not initialized")
	}

	ctx := context.Background()
	return redisConnection.SMembers(ctx, key).Result()
}
