// Package redistest provides isolated Redis clients for tests using
// per-test key namespaces.
package redistest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"go.segfaultmedaddy.com/redistest/internal/commandinfo"
)

const (
	cleanupTimeout   = 15 * time.Second
	cleanupScanCount = 500
	cleanupBatchSize = 500
)

// RedisFactory manages Redis clients used for isolated tests.
//
// Each client created by [RedisFactory.Client] transparently prefixes its key
// arguments with a unique namespace. A factory can be shared by parallel tests
// and reuses cached Redis command metadata across its clients.
type RedisFactory struct {
	mclient                 redis.UniversalClient
	opts                    *redis.UniversalOptions
	keyFinders              map[string]*commandinfo.KeyFinder
	factoryPrefix           string
	mu                      sync.Mutex
	ctn                     atomic.Uint64
	shouldKeepKeysOnFailure bool
}

// Option configures a [RedisFactory].
type Option func(*RedisFactory)

// WithOptions sets the [redis.UniversalOptions] used by the factory's metadata
// client and every test client it creates.
//
// The options must not be modified after the factory is created.
func WithOptions(opts *redis.UniversalOptions) Option {
	return func(f *RedisFactory) {
		f.opts = opts
	}
}

// WithKeepKeysOnFailure controls whether keys are retained when a test fails.
// By default, keys are removed after both successful and failed tests.
func WithKeepKeysOnFailure(isKeepKeysOnFailureEnabled bool) Option {
	return func(f *RedisFactory) {
		f.shouldKeepKeysOnFailure = isKeepKeysOnFailureEnabled
	}
}

// NewFactory creates a new [RedisFactory].
//
// NewFactory generates a random factory-level key prefix and returns an error
// if the prefix cannot be generated. The factory does not connect to Redis
// until one of its clients executes a command. If WithOptions is not provided,
// go-redis defaults are used.
func NewFactory(opts ...Option) (*RedisFactory, error) {
	f := RedisFactory{
		mclient:                 nil,
		opts:                    nil,
		keyFinders:              make(map[string]*commandinfo.KeyFinder),
		factoryPrefix:           "",
		mu:                      sync.Mutex{},
		ctn:                     atomic.Uint64{},
		shouldKeepKeysOnFailure: false,
	}
	for _, opt := range opts {
		opt(&f)
	}

	if f.opts == nil {
		f.opts = &redis.UniversalOptions{}
	}

	prefix := make([]byte, 16)

	_, err := rand.Read(prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to read random bytes for factory key prefix: %w", err)
	}

	f.factoryPrefix = hex.EncodeToString(prefix)
	f.mclient = redis.NewUniversalClient(f.opts)

	return &f, nil
}

// Client returns a Redis client and physical prefix with a unique key
// namespace for tb.
//
// The client can be used like any other [redis.UniversalClient]. Redis key
// arguments are prefixed automatically, including commands executed in a
// pipeline. The prefix contains no Redis glob metacharacters and can be
// prepended to a logical glob pattern to scope commands such as SCAN that do
// not declare key arguments. The client is closed when the test finishes.
//
// Namespaced keys are unlinked during cleanup. When
// [WithKeepKeysOnFailure] is enabled, keys are instead left intact after a
// failed test so that its Redis state can be inspected.
func (f *RedisFactory) Client(tb testing.TB) (redis.UniversalClient, string) {
	tb.Helper()

	client, prefix := f.newClient(tb.Name())
	cleanupPattern := prefix + "*"

	tb.Cleanup(func() {
		defer func() {
			if err := client.Close(); err != nil {
				tb.Logf("failed to close test client for test %q: %v", tb.Name(), err)
			}
		}()

		if f.shouldKeepKeysOnFailure && tb.Failed() {
			tb.Logf("failed test, leaving Redis keys matching %q intact", cleanupPattern)

			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		if err := f.cleanupKeys(ctx, cleanupPattern); err != nil {
			tb.Logf("failed to clean up Redis keys for test %q: %v", tb.Name(), err)
		}
	})

	return client, prefix
}

func (f *RedisFactory) newClient(testName string) (redis.UniversalClient, string) {
	escapedTestName := strings.NewReplacer(
		`\`, `%5C`,
		`*`, `%2A`,
		`?`, `%3F`,
		`[`, `%5B`,
	).Replace(url.PathEscape(testName))
	prefix := f.factoryPrefix + ":" + escapedTestName + ":" + strconv.FormatUint(f.ctn.Add(1), 10) + ":"

	client := redis.NewUniversalClient(f.opts)
	client.AddHook(newPrefixHook(prefix, f))

	return client, prefix
}

func (f *RedisFactory) keyFinder(
	ctx context.Context,
	cmdName string,
) (*commandinfo.KeyFinder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if keyFinder, hasKeyFinder := f.keyFinders[cmdName]; hasKeyFinder {
		return keyFinder, nil
	}

	cmd := commandinfo.New(f.mclient)

	keyFinder, err := cmd.KeyFinder(ctx, cmdName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve key finder for command %s: %w", cmdName, err)
	}

	f.keyFinders[cmdName] = keyFinder

	return keyFinder, nil
}

func (f *RedisFactory) cleanupKeys(ctx context.Context, match string) error {
	var cursor uint64

	for {
		keys, nextCursor, err := f.mclient.Scan(ctx, cursor, match, cleanupScanCount).Result()
		if err != nil {
			return fmt.Errorf("failed to scan keys matching %q: %w", match, err)
		}

		for start := 0; start < len(keys); start += cleanupBatchSize {
			end := min(start+cleanupBatchSize, len(keys))
			if err := f.mclient.Unlink(ctx, keys[start:end]...).Err(); err != nil {
				return fmt.Errorf("failed to unlink keys matching %q: %w", match, err)
			}
		}

		if nextCursor == 0 {
			return nil
		}

		cursor = nextCursor
	}
}
