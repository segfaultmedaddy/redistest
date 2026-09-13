# redistest

Isolated Redis testing for Go using namespaced keys.

redistest provides isolated test environments for Redis-backed functionality
in Go. It creates standard [go-redis](https://github.com/redis/go-redis)
clients with a separate view of Redis keys for each test, allowing tests to
share a Redis server without sharing state.

## Usage

```sh
$ go get go.segfaultmedaddy.com/redistest
```

### 1. Initialize the factory

Create one factory with the Redis connection options and share it across the
test suite:

```go
var factory *redistest.RedisFactory

func TestMain(m *testing.M) {
    var err error
    factory, err = redistest.NewFactory(
        redistest.WithOptions(&redis.UniversalOptions{
            Addrs: []string{"localhost:6379"},
        }),
    )
    if err != nil {
        panic(err)
    }

    os.Exit(m.Run())
}
```

### 2. Write isolated tests

Request a separate client for each test. The client is automatically cleaned
up and closed when the test completes.

```go
func TestUsers(t *testing.T) {
    t.Parallel()

    ctx := context.Background()
    client := factory.Client(t)

    err := client.Set(ctx, "user:1", "Alice", 0).Err()
    require.NoError(t, err)

    name, err := client.Get(ctx, "user:1").Result()
    require.NoError(t, err)
    require.Equal(t, "Alice", name)
}
```

## Behavior

Call `factory.Client(t)` once for each test and use the result like any other
`redis.UniversalClient`. Separate tests can use the same logical key names,
including while running in parallel, without reading or modifying each other's
values. Key-based commands, multi-key commands, pipelines, and transactions
use the test's isolated keyspace automatically.

When a test succeeds, redistest removes its keys and closes its client. Cleanup
errors are written to the test log without failing the test. When a test fails,
its keys are left in Redis for debugging and the matching key pattern is
written to the test log.

The factory should normally be created once in `TestMain` and shared by the
entire test suite. Multiple test binaries can use the same Redis server at the
same time without sharing their isolated keys.

redistest requires Redis 7 or newer and Redis credentials that permit
`COMMAND INFO`. A command returns an error when Redis cannot provide complete
information about its key arguments.

Some Redis operations are not based on keys and therefore are not isolated:

- Server-wide and keyspace-inspection commands such as `FLUSHDB`, `DBSIZE`,
  `KEYS`, and `SCAN` still operate on the shared Redis database.
- Pub/Sub channels are shared because channel names are not Redis keys.
- Scripts must declare every accessed key through their `KEYS` arguments.
- A process crash or timeout can leave test keys behind. Use expirations or a
  disposable Redis instance if retained data is a concern.

The package is deliberately built on top of
[go-redis](https://github.com/redis/go-redis) and returns its
`redis.UniversalClient` interface.

## Development

The project uses [devenv](https://devenv.sh/) and [direnv](https://direnv.net/) to provide Go tooling and a local Redis server.

```sh
direnv allow
devenv tasks run go:test-ci
```

## License

MIT Licensed
