package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/redis/go-redis/v9"
)

// redisClientConfig holds connection details for a Redis client.
type redisClientConfig struct {
	// Address is the Redis server address in the form hostname:port.
	Address string `envconfig:"REDIS_ADDRESS" required:"true"`
	// Password for Redis server authentication.
	Password string `envconfig:"REDIS_PASSWORD"`
	// DB is the database number to use.
	DB int `envconfig:"REDIS_DB" default:"0"`
}

// RedisClientFromEnv returns a *redis.Client configured with values
// from environment variables.
func RedisClientFromEnv() (*redis.Client, error) {
	cfg := redisClientConfig{}
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Address,
		Password: cfg.Password,
		DB:       cfg.DB,
	}), nil
}

// redisCache is a Redis-based implementation of the Cache interface.
type redisCache[V any] struct {
	// client is the internal client for Redis.
	client *redis.Client
}

// NewRedisCache returns a Redis-based implementation of the Cache interface
// with the specified configuration. In the event of and error, a nil is
// returned along with the error.
func NewRedisCache[V any](client *redis.Client) (Cache[V], error) {
	// Test the connection
	//
	// TODO(krancour): Make the timeout on the test configurable?
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf(
			"error testing connection to Redis server: %w", err,
		)
	}
	return &redisCache[V]{client: client}, nil
}

// Get implements Cache.
func (c *redisCache[V]) Get(ctx context.Context, key string) (V, bool, error) {
	var value V
	data, err := c.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return value, false, nil // Not found
		}
		return value, false,
			fmt.Errorf("error retrieving cached value for key %q: %w", key, err)
	}
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		return value, false,
			fmt.Errorf("error unmarshaling cached value of key %q: %w", key, err)
	}
	return value, true, nil
}

// Set implements Cache.
func (c *redisCache[V]) Set(ctx context.Context, key string, value V) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("error marshaling value for key %q: %w", key, err)
	}
	return c.client.Set(ctx, key, data, 0).Err() // Key does not expire
}
