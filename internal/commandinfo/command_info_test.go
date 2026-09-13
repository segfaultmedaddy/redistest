package commandinfo_test

import (
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.segfaultmedaddy.com/redistest/internal/commandinfo"
)

func Test_KeyFinder_Indexes(t *testing.T) {
	t.Parallel()

	redisAddress := os.Getenv("TEST_REDIS_ADDRESS")
	client := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{redisAddress}})
	require.NoError(t, client.Ping(t.Context()).Err())

	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Logf("failed to close Redis client: %v", err)
		}
	})

	tests := []struct {
		name            string
		command         string
		args            []any
		expectedIndexes []int
	}{
		{
			name:            "should find the key when APPEND has a fixed key position",
			command:         "APPEND",
			args:            []any{"APPEND", "key", "value"},
			expectedIndexes: []int{1},
		},
		{
			name:            "should find alternating keys when MSET has key-value pairs",
			command:         "MSET",
			args:            []any{"MSET", "one", "value-one", "two", "value-two", "three", "value-three"},
			expectedIndexes: []int{1, 3, 5},
		},
		{
			name:            "should find contiguous keys when MGET has multiple keys",
			command:         "MGET",
			args:            []any{"MGET", "one", "two", "three"},
			expectedIndexes: []int{1, 2, 3},
		},
		{
			name:            "should find the declared number of keys when EVAL specifies numkeys",
			command:         "EVAL",
			args:            []any{"EVAL", "return ARGV[1]", 2, "one", "two", "argument"},
			expectedIndexes: []int{3, 4},
		},
		{
			name:            "should find the destination and declared source keys when ZUNIONSTORE has numkeys",
			command:         "ZUNIONSTORE",
			args:            []any{"ZUNIONSTORE", "destination", 2, "one", "two", "WEIGHTS", 1, 2},
			expectedIndexes: []int{1, 3, 4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			commandInfo := commandinfo.New(client)
			finder, err := commandInfo.KeyFinder(t.Context(), tt.command)
			require.NoError(t, err)

			expectedIndexes := tt.expectedIndexes

			// Act
			actualIndexes, err := finder.Indexes(t.Context(), tt.args)
			require.NoError(t, err)

			// Assert
			assert.ElementsMatch(t, expectedIndexes, actualIndexes)
		})
	}
}
