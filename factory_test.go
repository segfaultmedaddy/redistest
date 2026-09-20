package redistest_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"go.segfaultmedaddy.com/redistest"
)

type deterministicPrefixer struct{}

func (deterministicPrefixer) Prefix(testName string) string {
	return "redistest:" + url.PathEscape(testName) + ":"
}

type cleanupTB struct {
	testing.TB

	name     string
	cleanups []func()
	failed   bool
}

func (tb *cleanupTB) Cleanup(cleanup func()) {
	tb.cleanups = append(tb.cleanups, cleanup)
}

func (tb *cleanupTB) Failed() bool {
	return tb.failed
}

func (tb *cleanupTB) Helper() {}

func (tb *cleanupTB) Logf(string, ...any) {}

func (tb *cleanupTB) Name() string {
	return tb.name
}

func (tb *cleanupTB) runCleanups() {
	for _, cleanup := range slices.Backward(tb.cleanups) {
		cleanup()
	}
}

func Test_RedisFactory_Client_Cleanup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		failed                 bool
		keepKeysOnFailure      bool
		configureKeepOnFailure bool
		expectedExists         int64
	}{
		{
			name:           "should remove keys after a failed test by default",
			failed:         true,
			expectedExists: 0,
		},
		{
			name:                   "should keep keys after a failed test when configured",
			failed:                 true,
			keepKeysOnFailure:      true,
			configureKeepOnFailure: true,
			expectedExists:         1,
		},
		{
			name:                   "should remove keys after a successful test when configured",
			keepKeysOnFailure:      true,
			configureKeepOnFailure: true,
			expectedExists:         0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			redisAddress := os.Getenv("TEST_REDIS_ADDRESS")
			opts := &redis.UniversalOptions{Addrs: []string{redisAddress}}
			prefixer := deterministicPrefixer{}

			factoryOptions := []redistest.Option{
				redistest.WithOptions(opts),
				redistest.WithPrefixer(prefixer),
			}
			if tt.configureKeepOnFailure {
				factoryOptions = append(
					factoryOptions,
					redistest.WithKeepKeysOnFailure(tt.keepKeysOnFailure),
				)
			}

			factory, err := redistest.NewFactory(factoryOptions...)
			require.NoError(t, err)

			admin := redis.NewUniversalClient(opts)
			require.NoError(t, admin.Ping(t.Context()).Err())

			tb := &cleanupTB{name: t.Name(), failed: tt.failed}
			client := factory.Client(tb)
			physicalKey := prefixer.Prefix(tb.Name()) + "1:key"

			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				if err := admin.Unlink(ctx, physicalKey).Err(); err != nil {
					t.Logf("failed to unlink test key: %v", err)
				}

				if err := admin.Close(); err != nil {
					t.Logf("failed to close Redis client: %v", err)
				}
			})

			require.NoError(t, client.Set(t.Context(), "key", "value", 0).Err())

			// Act
			tb.runCleanups()

			actualExists, err := admin.Exists(t.Context(), physicalKey).Result()
			require.NoError(t, err)

			// Assert
			assert.Equal(t, tt.expectedExists, actualExists)
		})
	}
}

func Test_RedisFactory_Client_Pipeline(t *testing.T) {
	t.Parallel()

	pipelines := []struct {
		new  func(redis.UniversalClient) redis.Pipeliner
		mode string
	}{
		{
			mode: "non-transactional",
			new:  func(client redis.UniversalClient) redis.Pipeliner { return client.Pipeline() },
		},
		{
			mode: "transactional",
			new:  func(client redis.UniversalClient) redis.Pipeliner { return client.TxPipeline() },
		},
	}

	for _, tt := range pipelines {
		t.Run(
			fmt.Sprintf(
				"should prefix keys when multiple commands are pipelined (%s)",
				tt.mode,
			),
			func(t *testing.T) {
				t.Parallel()

				// Arrange
				redisAddress := os.Getenv("TEST_REDIS_ADDRESS")
				opts := &redis.UniversalOptions{Addrs: []string{redisAddress}}
				prefixer := deterministicPrefixer{}
				factory, err := redistest.NewFactory(
					redistest.WithOptions(opts),
					redistest.WithPrefixer(prefixer),
				)
				require.NoError(t, err)
				require.NotNil(t, factory)

				admin := redis.NewUniversalClient(opts)
				require.NoError(t, admin.Ping(t.Context()).Err())

				prefix := prefixer.Prefix(t.Name()) + "1:"
				physicalKeys := []string{prefix + "one", prefix + "two"}

				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()

					if err := admin.Unlink(ctx, physicalKeys...).Err(); err != nil {
						t.Logf("failed to unlink test keys: %v", err)
					}

					if err := admin.Close(); err != nil {
						t.Logf("failed to close Redis client: %v", err)
					}
				})

				client := factory.Client(t)
				pipe := tt.new(client)
				expectedSetResult := "OK"
				expectedAppendResult := int64(9)
				expectedFirstValue := "value-one"
				expectedSecondValue := "value-two"
				expectedValues := []any{expectedFirstValue, expectedSecondValue}
				setCmd := pipe.Set(t.Context(), "one", expectedFirstValue, 0)
				appendCmd := pipe.Append(t.Context(), "two", expectedSecondValue)
				mgetCmd := pipe.MGet(t.Context(), "one", "two")

				// Act
				_, execErr := pipe.Exec(t.Context())
				require.NoError(t, execErr)

				actualSetResult, setErr := setCmd.Result()
				require.NoError(t, setErr)

				actualAppendResult, appendErr := appendCmd.Result()
				require.NoError(t, appendErr)

				actualValues, mgetErr := mgetCmd.Result()
				require.NoError(t, mgetErr)

				actualPhysicalValues, physicalMGetErr := admin.MGet(
					t.Context(),
					physicalKeys...,
				).Result()
				require.NoError(t, physicalMGetErr)

				// Assert
				assert.Equal(t, expectedSetResult, actualSetResult)
				assert.Equal(t, expectedAppendResult, actualAppendResult)
				assert.Equal(t, expectedValues, actualValues)
				assert.Equal(t, expectedValues, actualPhysicalValues)
			},
		)
	}
}

func Test_RedisFactory_Client(t *testing.T) {
	t.Parallel()

	// Arrange
	redisAddress := os.Getenv("TEST_REDIS_ADDRESS")
	opts := &redis.UniversalOptions{Addrs: []string{redisAddress}}
	prefixer := deterministicPrefixer{}
	admin := redis.NewUniversalClient(opts)
	require.NoError(t, admin.Ping(t.Context()).Err())

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		keys, err := admin.Keys(ctx, "redistest:"+url.PathEscape(t.Name())+"*").Result()
		if err != nil {
			t.Logf("failed to find test keys: %v", err)
		} else if len(keys) > 0 {
			if err := admin.Unlink(ctx, keys...).Err(); err != nil {
				t.Logf("failed to unlink test keys: %v", err)
			}
		}

		if err := admin.Close(); err != nil {
			t.Logf("failed to close Redis client: %v", err)
		}
	})

	t.Run("should isolate keys when clients are used concurrently", func(t *testing.T) {
		t.Parallel()

		// Arrange
		const clientCount = 10

		prefixRoot := "redistest:" + url.PathEscape(t.Name()) + ":"
		concurrentFactory, err := redistest.NewFactory(
			redistest.WithOptions(opts),
			redistest.WithPrefixer(deterministicPrefixer{}),
		)
		require.NoError(t, err)

		clients := make([]redis.UniversalClient, clientCount)
		expectedValues := make([]string, clientCount)
		physicalKeys := make([]string, clientCount)

		for i := range clientCount {
			clients[i] = concurrentFactory.Client(t)
			expectedValues[i] = strconv.Itoa(i)
			physicalKeys[i] = prefixRoot + strconv.Itoa(i+1) + ":shared-key"
		}

		results := make([]struct {
			actualPhysicalValue string
			actualLogicalValue  string
		}, clientCount)
		group, ctx := errgroup.WithContext(t.Context())

		// Act
		for i, client := range clients {
			group.Go(func() error {
				if err := client.Set(ctx, "shared-key", expectedValues[i], 0).Err(); err != nil {
					return fmt.Errorf("failed to set value for client %d: %w", i, err)
				}

				var err error

				results[i].actualPhysicalValue, err = admin.Get(ctx, physicalKeys[i]).Result()
				if err != nil {
					return fmt.Errorf("failed to get physical value for client %d: %w", i, err)
				}

				results[i].actualLogicalValue, err = client.Get(ctx, "shared-key").Result()
				if err != nil {
					return fmt.Errorf("failed to get logical value for client %d: %w", i, err)
				}

				return nil
			})
		}

		actualError := group.Wait()
		require.NoError(t, actualError)

		// Assert
		for i, result := range results {
			assert.Equal(t, expectedValues[i], result.actualPhysicalValue)
			assert.Equal(t, expectedValues[i], result.actualLogicalValue)
		}
	})

	commandTests := []struct {
		run  func(*testing.T, redis.UniversalClient, redis.UniversalClient, string)
		name string
	}{
		{
			name: "APPEND",
			run: func(t *testing.T, client, admin redis.UniversalClient, prefix string) {
				t.Helper()

				// Arrange
				key := "append"
				expectedResult := int64(5)
				expectedValue := "value"

				// Act
				actualResult, appendErr := client.Append(t.Context(), key, expectedValue).Result()
				require.NoError(t, appendErr)

				actualValue, getErr := admin.Get(t.Context(), prefix+key).Result()
				require.NoError(t, getErr)

				// Assert
				assert.Equal(t, expectedResult, actualResult)
				assert.Equal(t, expectedValue, actualValue)
			},
		},
		{
			name: "GETDEL",
			run: func(t *testing.T, client, admin redis.UniversalClient, prefix string) {
				t.Helper()

				// Arrange
				key := "getdel"
				expectedResult := "value"
				expectedExists := int64(0)

				require.NoError(t, admin.Set(t.Context(), prefix+key, expectedResult, 0).Err())

				// Act
				actualResult, getDelErr := client.GetDel(t.Context(), key).Result()
				require.NoError(t, getDelErr)

				actualExists, existsErr := admin.Exists(t.Context(), prefix+key).Result()
				require.NoError(t, existsErr)

				// Assert
				assert.Equal(t, expectedResult, actualResult)
				assert.Equal(t, expectedExists, actualExists)
			},
		},
		{
			name: "MSET",
			run: func(t *testing.T, client, admin redis.UniversalClient, prefix string) {
				t.Helper()

				// Arrange
				keys := []string{"one", "two"}
				expectedValues := []any{"value-one", "value-two"}

				// Act
				msetErr := client.MSet(
					t.Context(),
					keys[0], expectedValues[0],
					keys[1], expectedValues[1],
				).Err()
				require.NoError(t, msetErr)

				actualValues, mgetErr := admin.MGet(
					t.Context(),
					prefix+keys[0],
					prefix+keys[1],
				).Result()
				require.NoError(t, mgetErr)

				// Assert
				assert.Equal(t, expectedValues, actualValues)
			},
		},
		{
			name: "MGET",
			run: func(t *testing.T, client, admin redis.UniversalClient, prefix string) {
				t.Helper()

				// Arrange
				keys := []string{"one", "two"}
				expectedValues := []any{"value-one", "value-two"}
				require.NoError(t, admin.MSet(
					t.Context(),
					prefix+keys[0], expectedValues[0],
					prefix+keys[1], expectedValues[1],
				).Err())

				// Act
				actualValues, err := client.MGet(t.Context(), keys...).Result()
				require.NoError(t, err)

				// Assert
				assert.Equal(t, expectedValues, actualValues)
			},
		},
		{
			name: "UNLINK",
			run: func(t *testing.T, client, admin redis.UniversalClient, prefix string) {
				t.Helper()

				// Arrange
				keys := []string{"one", "two"}
				expectedResult := int64(2)
				expectedExists := int64(0)

				require.NoError(t, admin.MSet(
					t.Context(),
					prefix+keys[0], "value-one",
					prefix+keys[1], "value-two",
				).Err())

				// Act
				actualResult, unlinkErr := client.Unlink(t.Context(), keys...).Result()
				require.NoError(t, unlinkErr)

				actualExists, existsErr := admin.Exists(
					t.Context(),
					prefix+keys[0],
					prefix+keys[1],
				).Result()
				require.NoError(t, existsErr)

				// Assert
				assert.Equal(t, expectedResult, actualResult)
				assert.Equal(t, expectedExists, actualExists)
			},
		},
	}

	for _, tt := range commandTests {
		t.Run("should prefix keys for "+tt.name, func(t *testing.T) {
			t.Parallel()

			factory, err := redistest.NewFactory(
				redistest.WithOptions(opts),
				redistest.WithPrefixer(prefixer),
			)
			require.NoError(t, err)

			client := factory.Client(t)
			tt.run(t, client, admin, prefixer.Prefix(t.Name())+"1:")
		})
	}
}
