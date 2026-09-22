package redistest

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/redis/go-redis/v9"

	"go.segfaultmedaddy.com/redistest/internal/cast"
)

var _ redis.Hook = (*prefixHook)(nil)

// prefixHook adds a test namespace to every Redis key argument before a
// command is sent. It uses the factory's cached command metadata to identify
// keys without modifying non-key arguments.
type prefixHook struct {
	factory *RedisFactory
	ns      string
}

// newPrefixHook creates a prefixHook that applies ns to keys and uses factory
// to resolve Redis command key specifications.
func newPrefixHook(ns string, factory *RedisFactory) *prefixHook {
	return &prefixHook{
		ns:      ns,
		factory: factory,
	}
}

// DialHook implements [redis.Hook].
func (t *prefixHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

// ProcessHook implements [redis.Hook].
func (t *prefixHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := t.namespace(ctx, cmd); err != nil {
			cmd.SetErr(err)
			return err
		}

		return next(ctx, cmd)
	}
}

// ProcessPipelineHook implements [redis.Hook].
func (t *prefixHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if err := t.namespace(ctx, cmd); err != nil {
				cmd.SetErr(err)
				return err
			}
		}

		return next(ctx, cmds)
	}
}

func (t *prefixHook) namespace(ctx context.Context, cmder redis.Cmder) error {
	cmd := cmder.FullName()

	finder, err := t.factory.keyFinder(ctx, cmd)
	if err != nil {
		return fmt.Errorf("failed to resolve key finder for command %s: %w", cmd, err)
	}

	indexes, err := finder.Indexes(ctx, cmder.Args())
	if err != nil {
		return fmt.Errorf("failed to find key indexes for command %s: %w", cmd, err)
	}

	args := cmder.Args()
	for _, index := range indexes {
		arg := args[index]
		if value, isByteSlice := arg.([]byte); isByteSlice {
			if bytes.HasPrefix(value, []byte(t.ns)) {
				continue
			}

			result := make([]byte, 0, len(t.ns)+len(value))
			result = append(result, t.ns...)
			args[index] = append(result, value...)

			continue
		}

		var value cast.String
		if err := value.Cast(arg); err != nil {
			value = cast.String(fmt.Sprint(arg))
		}

		if !strings.HasPrefix(string(value), t.ns) {
			args[index] = t.ns + string(value)
		}
	}

	return nil
}
