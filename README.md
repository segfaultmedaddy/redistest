# redistest

Run Redis-backed Go tests in parallel without them interfering with each other.

redistest gives each test a standard
[go-redis](https://github.com/redis/go-redis) client with an isolated view of
Redis keys. Parallel tests can share one Redis server and reuse the same logical
key names without reading, overwriting, or deleting each other's data.

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

    ctx := t.Context()
    client, _ := factory.Client(t)

    err := client.Set(ctx, "user:1", "Alice", 0).Err()
    require.NoError(t, err)

    name, err := client.Get(ctx, "user:1").Result()
    require.NoError(t, err)
    require.Equal(t, "Alice", name)
}
```

## Behavior

`factory.Client(t)` creates a standard `redis.UniversalClient` with a namespace
unique to that client and returns its physical key prefix. The prefix contains
no Redis glob metacharacters and is safe to use in a `MATCH` pattern. Multiple
clients requested by the same test are isolated from each other. Before Redis
receives a command, redistest uses the server's command metadata to identify
its key arguments and prefixes only those keys. Pipelines and transactions use
the same process, so parallel tests can safely reuse logical key names while
storing data under different physical keys.

When a test completes, redistest removes the keys in its namespace and closes
the client. Pass `redistest.WithKeepKeysOnFailure(true)` to `NewFactory` to keep
the keys from failed tests for inspection. The client is still closed.

### Scoped scans

Use the returned prefix to scope `SCAN` to the client's keys. Append a logical
glob pattern directly; generated prefixes contain no Redis glob metacharacters:

```go
client, prefix := factory.Client(t)

iterator := client.Scan(t.Context(), 0, prefix+"user:*", 100).Iterator()
for iterator.Next(t.Context()) {
    key := iterator.Val() // SCAN returns the physical, namespaced key.
    // ...
}
require.NoError(t, iterator.Err())
```

### Unsupported commands

redistest supports commands whose key arguments Redis describes through
`COMMAND INFO`. For incomplete specifications, it asks Redis to resolve the
keys from the full command. If the key positions still cannot be determined
unambiguously, or Redis reports an unknown specification, redistest returns an
error without executing the command. The exact set depends on the Redis
version.

Commands without key arguments are sent unchanged and are therefore not
isolated. This includes database-wide commands such as `FLUSHDB`, `FLUSHALL`,
`DBSIZE`, `KEYS`, and `SCAN`, as well as Pub/Sub commands. Scripts and functions
must declare every key through their key arguments; keys constructed inside the
script are not prefixed.

## Development

The project uses [devenv](https://devenv.sh/) and [direnv](https://direnv.net/) to provide Go tooling and a local Redis server.

```sh
direnv allow
devenv tasks run go:test-ci
```

## License

MIT Licensed
