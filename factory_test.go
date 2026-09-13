package redistest_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.segfaultmedaddy.com/redistest"
)

type deterministicPrefixer struct{}

func (deterministicPrefixer) Prefix(testName string) string {
	return "redistest:" + url.PathEscape(testName) + ":"
}

type incrementingPrefixer struct {
	root string
	next int
}

func (p *incrementingPrefixer) Prefix(_ string) string {
	p.next++

	return p.root + strconv.Itoa(p.next) + ":"
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

				prefix := prefixer.Prefix(t.Name())
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
	factory, err := redistest.NewFactory(
		redistest.WithOptions(opts),
		redistest.WithPrefixer(prefixer),
	)
	require.NoError(t, err)
	require.NotNil(t, factory)

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

		var wg sync.WaitGroup

		prefixRoot := "redistest:" + url.PathEscape(t.Name()) + ":"
		concurrentPrefixer := &incrementingPrefixer{root: prefixRoot}
		concurrentFactory, err := redistest.NewFactory(
			redistest.WithOptions(opts),
			redistest.WithPrefixer(concurrentPrefixer),
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
			setErr              error
			physicalErr         error
			logicalErr          error
			actualPhysicalValue string
			actualLogicalValue  string
		}, clientCount)
		start := make(chan struct{})

		// Act
		for i, client := range clients {
			wg.Go(func() {
				<-start

				results[i].setErr = client.Set(t.Context(), "shared-key", expectedValues[i], 0).Err()
				results[i].actualPhysicalValue, results[i].physicalErr = admin.Get(
					t.Context(),
					physicalKeys[i],
				).Result()
				results[i].actualLogicalValue, results[i].logicalErr = client.Get(
					t.Context(),
					"shared-key",
				).Result()
			})
		}

		close(start)
		wg.Wait()

		// Assert
		for i, result := range results {
			require.NoError(t, result.setErr)
			require.NoError(t, result.physicalErr)
			require.NoError(t, result.logicalErr)
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

			client := factory.Client(t)
			tt.run(t, client, admin, prefixer.Prefix(t.Name()))
		})
	}
}
