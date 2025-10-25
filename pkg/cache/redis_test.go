package cache

import (
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisClientFromEnv(t *testing.T) {
	t.Run("REDIS_ADDRESS env var undefined", func(t *testing.T) {
		_, err := RedisClientFromEnv()
		require.ErrorContains(t, err, "REDIS_ADDRESS missing value")
	})
	t.Run("success", func(t *testing.T) {
		t.Setenv("REDIS_ADDRESS", "localhost:6379")
		client, err := RedisClientFromEnv()
		require.NoError(t, err)
		require.NotNil(t, client)
		require.IsType(t, &redis.Client{}, client)
	})
}

func TestNewRedisCache(t *testing.T) {
	testCases := []struct {
		name      string
		setup     func(t *testing.T) *redis.Client
		expectErr bool
	}{
		{
			name: "failed connection",
			setup: func(_ *testing.T) *redis.Client {
				return redis.NewClient(&redis.Options{Addr: "invalid:9999"})
			},
			expectErr: true,
		},
		{
			name: "successful connection",
			setup: func(t *testing.T) *redis.Client {
				s := miniredis.RunT(t)
				return redis.NewClient(&redis.Options{Addr: s.Addr()})
			},
			expectErr: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			client := testCase.setup(t)
			defer client.Close()
			cache, err := NewRedisCache[string](client)
			if testCase.expectErr {
				require.Error(t, err)
				require.Nil(t, cache)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cache)
				redisCache, ok := cache.(*redisCache[string])
				require.True(t, ok)
				require.NotNil(t, redisCache.client)
			}
		})
	}
}

func TestRedisCache_Get(t *testing.T) {
	const testKey = "key"

	type testStruct struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	alice := testStruct{Name: "Alice", Age: 30}

	testCases := []struct {
		name       string
		setup      func(*testing.T) *redis.Client
		assertions func(*testing.T, testStruct, bool, error)
	}{
		{
			name: "key not found",
			setup: func(t *testing.T) *redis.Client {
				return redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
			},
			assertions: func(t *testing.T, _ testStruct, found bool, err error) {
				require.NoError(t, err)
				require.False(t, found)
			},
		},
		{
			name: "errors unmarshaling JSON",
			setup: func(t *testing.T) *redis.Client {
				client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
				// This cannot be unmarshaled into testStruct
				client.Set(t.Context(), testKey, "invalid", 0)
				return client
			},
			assertions: func(t *testing.T, _ testStruct, _ bool, err error) {
				require.ErrorContains(t, err, "error unmarshaling cached value")
			},
		},
		{
			name: "success",
			setup: func(t *testing.T) *redis.Client {
				data, err := json.Marshal(alice)
				require.NoError(t, err)
				client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
				client.Set(t.Context(), testKey, string(data), 0)
				return client
			},
			assertions: func(t *testing.T, val testStruct, found bool, err error) {
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, alice, val)
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cache := &redisCache[testStruct]{client: tc.setup(t)}
			val, found, err := cache.Get(t.Context(), testKey)
			tc.assertions(t, val, found, err)
		})
	}
}

func TestRedisCache_Set(t *testing.T) {
	const testKey = "key"

	type testStruct struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	alice := testStruct{Name: "Alice", Age: 30}

	bob := testStruct{Name: "Bob", Age: 25}

	testCases := []struct {
		name       string
		setup      func(*testing.T) *redis.Client
		value      testStruct
		assertions func(*testing.T, *redis.Client, error)
	}{
		{
			name: "initial write",
			setup: func(t *testing.T) *redis.Client {
				return redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
			},
			value: alice,
			assertions: func(t *testing.T, c *redis.Client, err error) {
				require.NoError(t, err)
				data, err := c.Get(t.Context(), testKey).Result()
				require.NoError(t, err)
				var value testStruct
				err = json.Unmarshal([]byte(data), &value)
				require.NoError(t, err)
				require.Equal(t, alice, value)
			},
		},
		{
			name: "overwrite",
			setup: func(t *testing.T) *redis.Client {
				client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
				data, err := json.Marshal(alice)
				require.NoError(t, err)
				client.Set(t.Context(), testKey, string(data), 0)
				return client
			},
			value: bob,
			assertions: func(t *testing.T, c *redis.Client, err error) {
				require.NoError(t, err)
				data, err := c.Get(t.Context(), testKey).Result()
				require.NoError(t, err)
				var value testStruct
				err = json.Unmarshal([]byte(data), &value)
				require.NoError(t, err)
				require.Equal(t, bob, value)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cache := &redisCache[testStruct]{client: testCase.setup(t)}
			err := cache.Set(t.Context(), testKey, testCase.value)
			testCase.assertions(t, cache.client, err)
		})
	}
}
