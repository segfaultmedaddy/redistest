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
// arguments with a namespace supplied by the configured [Prefixer]. A factory
// can be shared by parallel tests and reuses cached Redis command metadata
// across its clients.
type RedisFactory struct {
	mclient           redis.UniversalClient
	prefixer          Prefixer
	opts              *redis.UniversalOptions
	keyFinders        map[string]*commandinfo.KeyFinder
	mu                sync.Mutex
	ctn               atomic.Uint64
	keepKeysOnFailure bool
}

// Prefixer creates the key prefix used to isolate a test.
//
// Prefix must return a non-empty prefix that uniquely identifies testName.
type Prefixer interface {
	Prefix(testName string) string
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

// WithPrefixer sets the [Prefixer] used to create test key prefixes.
func WithPrefixer(prefixer Prefixer) Option {
	return func(f *RedisFactory) {
		f.prefixer = prefixer
	}
}

// WithKeepKeysOnFailure controls whether keys are retained when a test fails.
// By default, keys are removed after both successful and failed tests.
func WithKeepKeysOnFailure(keep bool) Option {
	return func(f *RedisFactory) {
		f.keepKeysOnFailure = keep
	}
}

// NewFactory creates a new [RedisFactory].
//
// Unless [WithPrefixer] is provided, NewFactory generates a random
// process-level key prefix. It returns an error if the prefix cannot be
// generated. The factory does not connect to Redis until one of its clients
// executes a command. If WithOptions is not provided, go-redis defaults are
// used.
func NewFactory(opts ...Option) (*RedisFactory, error) {
	f := RedisFactory{
		mclient:           nil,
		prefixer:          nil,
		opts:              nil,
		keyFinders:        make(map[string]*commandinfo.KeyFinder),
		mu:                sync.Mutex{},
		ctn:               atomic.Uint64{},
		keepKeysOnFailure: false,
	}
	for _, opt := range opts {
		opt(&f)
	}

	if f.opts == nil {
		f.opts = &redis.UniversalOptions{}
	}

	if f.prefixer == nil {
		prefix := make([]byte, 16)

		_, err := rand.Read(prefix)
		if err != nil {
			return nil, fmt.Errorf("failed to read random bytes for process key prefix: %w", err)
		}

		f.prefixer = defaultPrefixer(hex.EncodeToString(prefix))
	}

	f.mclient = redis.NewUniversalClient(f.opts)

	return &f, nil
}

// Client returns a Redis client with a unique key namespace for tb.
//
// The client can be used like any other [redis.UniversalClient]. Redis key
// arguments are prefixed automatically, including commands executed in a
// pipeline. The client is closed when the test finishes.
//
// Namespaced keys are unlinked during cleanup. When
// [WithKeepKeysOnFailure] is enabled, keys are instead left intact after a
// failed test so that its Redis state can be inspected.
func (f *RedisFactory) Client(tb testing.TB) redis.UniversalClient {
	tb.Helper()

	client, cleanupPattern := f.newClient(tb.Name())

	tb.Cleanup(func() {
		defer func() {
			if err := client.Close(); err != nil {
				tb.Logf("failed to close test client for test %q: %v", tb.Name(), err)
			}
		}()

		if f.keepKeysOnFailure && tb.Failed() {
			tb.Logf("failed test, leaving Redis keys matching %q intact", cleanupPattern)

			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		if err := f.cleanupKeys(ctx, cleanupPattern); err != nil {
			tb.Logf("failed to clean up Redis keys for test %q: %v", tb.Name(), err)
		}
	})

	return client
}

func (f *RedisFactory) newClient(testName string) (redis.UniversalClient, string) {
	ns := f.prefixer.Prefix(testName) + strconv.FormatUint(f.ctn.Add(1), 10) + ":"
	cleanupPattern := strings.NewReplacer(
		`\`, `\\`,
		`*`, `\*`,
		`?`, `\?`,
		`[`, `\[`,
	).Replace(ns) + "*"

	client := redis.NewUniversalClient(f.opts)
	client.AddHook(newPrefixHook(ns, f))

	return client, cleanupPattern
}

func (f *RedisFactory) keyFinder(
	ctx context.Context,
	cmdName string,
) (*commandinfo.KeyFinder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if keyFinder, ok := f.keyFinders[cmdName]; ok {
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

type defaultPrefixer string

func (p defaultPrefixer) Prefix(testName string) string {
	return fmt.Sprintf("%s:%s:", p, url.PathEscape(testName))
}
