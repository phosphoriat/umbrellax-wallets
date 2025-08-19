package redis

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"wallets/internal/config"

	"github.com/redis/go-redis/v9"
)

// Client Redis connection struct
type Client struct {
	redis *redis.Client
	mu    sync.RWMutex
}

// Global package vars
var (
	clientInstance *Client
	once           sync.Once
)

// GetClient Get global Redis Client instance
func GetClient() *Client {
	return clientInstance
}

// Init Redis connection (singleton)
func Init(cfg *config.Config) error {
	var err error
	once.Do(func() {
		clientInstance, err = newRedisClient(cfg)
	})
	return err
}

// Close global Redis connection
func Close() error {
	if clientInstance == nil {
		return nil
	}
	return clientInstance.closeConnection()
}

// @private newRedisClient Create new Redis Client instance (private constructor)
func newRedisClient(cfg *config.Config) (*Client, error) {
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
		client.Close()
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}
	if pong != "PONG" {
		client.Close()
		return nil, fmt.Errorf("unexpected ping response from Redis: %s", pong)
	}

	log.Printf("Redis connection established: %s:%s (DB: %d)",
		cfg.RedisHost, cfg.RedisPort, cfg.RedisDatabase)

	return &Client{
		redis: client,
	}, nil
}

// @private getClient Get Redis connection instance
func (c *Client) getClient() (*redis.Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.redis == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}

	return c.redis, nil
}

// @private closeConnection Close Redis connection
func (c *Client) closeConnection() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.redis != nil {
		err := c.redis.Close()
		c.redis = nil
		return err
	}

	return nil
}

// Set string key value with TTL
func (c *Client) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.Set(ctx, key, value, expiration).Err()
}

// Get string key value from Redis
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}

	return client.Get(ctx, key).Result()
}

// GetWithExpiration Get key value with TTL
func (c *Client) GetWithExpiration(ctx context.Context, key string) (string, time.Duration, error) {
	client, err := c.getClient()
	if err != nil {
		return "", 0, err
	}

	pipe := client.Pipeline()
	getCmd := pipe.Get(ctx, key)
	ttlCmd := pipe.TTL(ctx, key)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return "", 0, err
	}

	value, err := getCmd.Result()
	if err != nil {
		return "", 0, err
	}

	ttl, err := ttlCmd.Result()
	if err != nil {
		return value, 0, err
	}

	return value, ttl, nil
}

// Del Delete key from Redis
func (c *Client) Del(ctx context.Context, keys ...string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.Del(ctx, keys...).Err()
}

// Exists Check if key exists
func (c *Client) Exists(ctx context.Context, keys ...string) (int64, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, err
	}

	return client.Exists(ctx, keys...).Result()
}

// Expire Set key expire time (TTL)
func (c *Client) Expire(ctx context.Context, key string, expiration time.Duration) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.Expire(ctx, key, expiration).Err()
}

// TTL Get key expire time (TTL)
func (c *Client) TTL(ctx context.Context, key string) (time.Duration, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, err
	}

	return client.TTL(ctx, key).Result()
}

// HSet Set hash field value
func (c *Client) HSet(ctx context.Context, key, field string, value interface{}) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.HSet(ctx, key, field, value).Err()
}

// HMSet Set hash multiple field values
func (c *Client) HMSet(ctx context.Context, key string, values map[string]interface{}) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.HMSet(ctx, key, values).Err()
}

// HGet Get field value from hash
func (c *Client) HGet(ctx context.Context, key, field string) (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}

	return client.HGet(ctx, key, field).Result()
}

// HGetAll Get all field and values from hash
func (c *Client) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	return client.HGetAll(ctx, key).Result()
}

// HMGet Get multiple field values from hash
func (c *Client) HMGet(ctx context.Context, key string, fields ...string) ([]interface{}, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	return client.HMGet(ctx, key, fields...).Result()
}

// HDel Delete hash field
func (c *Client) HDel(ctx context.Context, key string, fields ...string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.HDel(ctx, key, fields...).Err()
}

// HExists Check if field exists in hash
func (c *Client) HExists(ctx context.Context, key, field string) (bool, error) {
	client, err := c.getClient()
	if err != nil {
		return false, err
	}

	return client.HExists(ctx, key, field).Result()
}

// HLen Get fields count in hash
func (c *Client) HLen(ctx context.Context, key string) (int64, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, err
	}

	return client.HLen(ctx, key).Result()
}

// GetFirstHashElement Get first element from hash
func (c *Client) GetFirstHashElement(ctx context.Context, key string) (string, string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", "", err
	}

	keys, _, err := client.HScan(ctx, key, 0, "", 1).Result()
	if err != nil {
		return "", "", err
	}

	if len(keys) < 2 {
		return "", "", fmt.Errorf("hash '%s' is empty or invalid", key)
	}

	return keys[0], keys[1], nil
}

// LPush Add values to the top of list
func (c *Client) LPush(ctx context.Context, key string, values ...interface{}) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.LPush(ctx, key, values...).Err()
}

// RPush Add values to the end of list
func (c *Client) RPush(ctx context.Context, key string, values ...interface{}) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.RPush(ctx, key, values...).Err()
}

// LPop Remove and return first value from list
func (c *Client) LPop(ctx context.Context, key string) (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}

	return client.LPop(ctx, key).Result()
}

// RPop Remove and return last value from list
func (c *Client) RPop(ctx context.Context, key string) (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}

	return client.RPop(ctx, key).Result()
}

// LLen Get list length
func (c *Client) LLen(ctx context.Context, key string) (int64, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, err
	}

	return client.LLen(ctx, key).Result()
}

// LRange Get values from list in passed range
func (c *Client) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	return client.LRange(ctx, key, start, stop).Result()
}

// LTrim Trim list for passed range
func (c *Client) LTrim(ctx context.Context, key string, start, stop int64) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.LTrim(ctx, key, start, stop).Err()
}

// SAdd Add values to set
func (c *Client) SAdd(ctx context.Context, key string, values ...interface{}) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.SAdd(ctx, key, values...).Err()
}

// SIsMember Check if value is in set
func (c *Client) SIsMember(ctx context.Context, key string, value interface{}) (bool, error) {
	client, err := c.getClient()
	if err != nil {
		return false, err
	}

	return client.SIsMember(ctx, key, value).Result()
}

// SMembers Get all set members
func (c *Client) SMembers(ctx context.Context, key string) ([]string, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	return client.SMembers(ctx, key).Result()
}

// SRem Remove values from set
func (c *Client) SRem(ctx context.Context, key string, values ...interface{}) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	return client.SRem(ctx, key, values...).Err()
}

// SCard Get values count in set
func (c *Client) SCard(ctx context.Context, key string) (int64, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, err
	}

	return client.SCard(ctx, key).Result()
}

// SPop Delete and return random value from set
func (c *Client) SPop(ctx context.Context, key string) (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}

	return client.SPop(ctx, key).Result()
}
