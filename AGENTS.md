# Development Guide

## Product Direction

- Optimize for the primary use case: Redis-backed Go tests running in parallel
  against one shared Redis server without interfering with each other.
- Keep redistest a thin layer over `go-redis`. Return standard
  `redis.UniversalClient` values and preserve normal go-redis behavior wherever
  isolation does not require intervention.
- Keep the public API small. Put protocol parsing, metadata handling, casting,
  and collection helpers under `internal/`.
- Prefer the smallest change that preserves these guarantees. Do not add a new
  abstraction or dependency without a concrete need.

## Isolation Model

- Give every test client a unique key namespace. A factory must remain safe to
  share across parallel tests and test binaries.
- Discover key arguments dynamically from Redis `COMMAND INFO`; do not maintain
  a hard-coded command catalog.
- Prefix only key arguments. Never alter command names, values, counts,
  keywords, or other non-key arguments.
- Apply identical namespacing behavior to individual commands, pipelines, and
  transactional pipelines.
- Keep namespacing idempotent and preserve key types, including `[]byte` keys.
- Reject incomplete or unknown key specifications before sending a command.
  Silently executing a command with partial isolation is worse than returning
  an explicit error.
- Commands without declared key arguments pass through unchanged and are not
  isolated. Scripts and functions isolate only keys supplied through their key
  arguments.
- On success, remove the test namespace and close the client. On failure, keep
  namespaced keys for inspection and still close the client. Log cleanup errors
  instead of changing the test result.

## Go Code

- Follow standard Go naming and documentation conventions. Add Go doc comments
  to exported identifiers and use documentation links such as
  `[RedisFactory.Client]`.
- Wrap errors with lowercase, operation-specific context and `%w`. Preserve
  sentinel errors so callers can use `errors.Is`.
- Return errors from library code rather than panicking. Include useful command
  names, indexes, field names, or key patterns in validation errors.
- Keep shared factory state concurrency-safe. Do not hold mutable global state.
- Add compile-time interface assertions when internal types implement external
  interfaces.
- Any `//nolint` directive must name the linter and explain the exception.
- Let the repository tooling define formatting and lint policy; do not hand-tune
  code against a conflicting local style.

## Tests

- Test public behavior from external test packages such as `redistest_test`.
- Name tests `Test_Type_Method` and use descriptive subtests beginning with
  `should`.
- Structure tests with `// Arrange`, `// Act`, and `// Assert` sections.
- Mark independent tests and subtests with `t.Parallel()`. Tests for isolation
  should exercise genuinely concurrent clients, not only sequential clients.
- Use `t.Context()` for normal test operations and bounded background contexts
  for cleanup that must run after the test context is canceled.
- Use Testify `require` for prerequisites and errors that make later assertions
  unsafe. Use `assert` for result comparisons, `ErrorIs` for sentinels, and
  `ElementsMatch` for unordered results.
- Never call `require` or other test-failing methods from worker goroutines.
  Return worker errors through `errgroup` and assert `group.Wait()` in the test
  goroutine.
- Redis integration tests use `TEST_REDIS_ADDRESS`, verify connectivity with
  `PING`, and use a separate admin client when physical namespaced keys must be
  inspected.
- Cover logical behavior, physical key rewriting, cleanup, failure retention,
  pipelines, transactions, unsupported metadata, and concurrent isolation when
  those paths change.

## Documentation

- Keep the README centered on parallel-test isolation, one shared factory, and
  one client per test.
- Explain behavior from a user's perspective. Document lifecycle or command
  support changes, especially cases that are rejected or pass through without
  isolation.
- Keep examples minimal and use `t.Context()` and `t.Parallel()`.

## Validation

- Use the devenv tasks as the source of truth for local and CI validation.
- Run `devenv tasks run go:lint` after Go changes.
- Run `devenv tasks run go:test-ci` for the full race-enabled suite with Redis.
- Run `devenv tasks run go:mod` after dependency changes.
- Format with `treefmt`; Go code is additionally governed by the formatters and
  linters configured in `.golangci.yaml`.
