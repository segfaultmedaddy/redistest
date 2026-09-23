// Package commandinfo resolves the key arguments used by Redis commands from
// the metadata reported by the Redis server.
package commandinfo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/redis/go-redis/v9"

	"go.segfaultmedaddy.com/redistest/internal/cast"
	"go.segfaultmedaddy.com/redistest/internal/resp"
	"go.segfaultmedaddy.com/redistest/internal/sliceutil"
)

// CommandInfo retrieves and interprets Redis command metadata.
type CommandInfo struct {
	client redis.UniversalClient
}

// New creates a CommandInfo backed by client.
func New(client redis.UniversalClient) *CommandInfo {
	return &CommandInfo{
		client: client,
	}
}

// KeyFinder returns a finder for the key arguments accepted by cmd.
func (c *CommandInfo) KeyFinder(ctx context.Context, cmd string) (*KeyFinder, error) {
	rawCmd := redis.NewRawCmd(ctx, "COMMAND", "INFO", cmd)
	if err := c.client.Process(ctx, rawCmd); err != nil {
		return nil, fmt.Errorf("failed to execute command info for %s: %w", cmd, err)
	}

	reply, err := rawCmd.Bytes()
	if err != nil {
		return nil, fmt.Errorf("failed to get command info response bytes for %s: %w", cmd, err)
	}

	m, err := resp.NewReader(bytes.NewReader(reply)).ReadReply()
	if err != nil {
		return nil, fmt.Errorf("failed to parse RESP3 command info for %s: %w", cmd, err)
	}

	finder, err := c.keyFinder(m)
	if err != nil {
		return nil, fmt.Errorf("failed to parse command info for %s: %w", cmd, err)
	}

	return finder, nil
}

func (c *CommandInfo) keyFinder(reply any) (*KeyFinder, error) {
	var commands cast.Array
	if err := commands.Cast(reply); err != nil {
		return nil, fmt.Errorf(
			"failed to cast COMMAND INFO reply to array for reply %T: %w",
			reply,
			err,
		)
	}

	if len(commands) != 1 {
		return nil, fmt.Errorf("expected one command, got %d", len(commands))
	}

	if commands[0] == nil {
		return nil, errors.New("expected command information, got nil")
	}

	_, finder, err := c.parseCommand(commands[0])

	return finder, err
}

func (c *CommandInfo) parseCommand(value any) (string, *KeyFinder, error) {
	var command cast.Array
	if err := command.Cast(value); err != nil {
		return "", nil, fmt.Errorf(
			"failed to cast command information to array for value %T: %w",
			value,
			err,
		)
	}

	if len(command) < 9 {
		return "", nil, fmt.Errorf(
			"expected at least 9 command information fields, got %d",
			len(command),
		)
	}

	var name cast.String
	if err := name.Cast(command[0]); err != nil {
		return "", nil, fmt.Errorf("failed to cast command name for value %T: %w", command[0], err)
	}

	var keySpecs cast.Array
	if err := keySpecs.Cast(command[8]); err != nil {
		return "", nil, fmt.Errorf(
			"failed to cast key specifications to array for value %T: %w",
			command[8],
			err,
		)
	}

	specs := make([]spec, 0, len(keySpecs))
	isDynamic := false

	for i, value := range keySpecs {
		keySpec, isIncomplete, err := parseKeySpec(value)
		if err != nil {
			return "", nil, fmt.Errorf("failed to parse key specification for index %d: %w", i, err)
		}

		isDynamic = isDynamic || isIncomplete

		if keySpec != nil {
			specs = append(specs, *keySpec)
		}
	}

	finder := &KeyFinder{
		client:      c.client,
		specs:       specs,
		isDynamic:   isDynamic,
		subcommands: nil,
	}
	if len(command) < 10 {
		return string(name), finder, nil
	}

	// Redis reports key specifications for commands such as XGROUP CREATE only
	// on nested subcommand metadata, so retain a finder for each subcommand.
	var subcommandValues cast.Array
	if err := subcommandValues.Cast(command[9]); err != nil {
		return "", nil, fmt.Errorf(
			"failed to cast subcommands to array for value %T: %w",
			command[9],
			err,
		)
	}

	finder.subcommands = make(map[string]*KeyFinder, len(subcommandValues))

	for i, subcommandValue := range subcommandValues {
		subcommandName, subcommandFinder, err := c.parseCommand(subcommandValue)
		if err != nil {
			return "", nil, fmt.Errorf("failed to parse subcommand for index %d: %w", i, err)
		}

		separator := strings.LastIndexByte(subcommandName, '|')
		if separator < 0 || separator == len(subcommandName)-1 {
			return "", nil, fmt.Errorf("expected qualified subcommand name, got %q", subcommandName)
		}

		finder.subcommands[strings.ToLower(subcommandName[separator+1:])] = subcommandFinder
	}

	return string(name), finder, nil
}

type spec struct {
	beginSearch beginSearchSpec
	findKeys    findKeysSpec
}

func (s *spec) indexes(args []any) ([]int, error) {
	start, isFound, err := s.beginSearch.start(args)
	if err != nil {
		return nil, err
	}

	if !isFound {
		return nil, nil
	}

	return s.findKeys.indexes(args, start)
}

type beginSearchSpec struct {
	typ       string
	keyword   string
	index     int
	startFrom int
}

func (s *beginSearchSpec) Cast(value any) error {
	typ, nested, err := typedSpec(value)
	if err != nil {
		return err
	}

	switch typ {
	case "index":
		index, err := parseField[cast.Int](nested, "index")
		if err != nil {
			return err
		}

		*s = beginSearchSpec{
			typ:       typ,
			keyword:   "",
			index:     int(index),
			startFrom: 0,
		}
	case "keyword":
		keyword, err := parseField[cast.String](nested, "keyword")
		if err != nil {
			return err
		}

		startFrom, err := parseField[cast.Int](nested, "startfrom")
		if err != nil {
			return err
		}

		*s = beginSearchSpec{
			typ:       typ,
			keyword:   string(keyword),
			index:     0,
			startFrom: int(startFrom),
		}
	default:
		return fmt.Errorf("begin search type %q is not supported: %w", typ, errors.ErrUnsupported)
	}

	return nil
}

func (s *beginSearchSpec) start(args []any) (int, bool, error) {
	switch s.typ {
	case "index":
		if s.index >= len(args) {
			return 0, false, fmt.Errorf(
				"expected begin search index in range [0, %d], got %d",
				len(args)-1,
				s.index,
			)
		}

		return s.index, true, nil
	case "keyword":
		if s.startFrom > 0 {
			if s.startFrom > len(args) {
				return 0, false, nil
			}

			for i := s.startFrom; i < len(args)-1; i++ {
				if isStringEqualFold(args[i], s.keyword) {
					return i + 1, true, nil
				}
			}

			return 0, false, nil
		}

		start := len(args) + s.startFrom
		for i := start; i > 1 && i < len(args); i-- {
			if isStringEqualFold(args[i], s.keyword) {
				return i + 1, true, nil
			}
		}

		return 0, false, nil
	default:
		return 0, false, fmt.Errorf(
			"begin search type %q is not supported: %w",
			s.typ,
			errors.ErrUnsupported,
		)
	}
}

type findKeysSpec struct {
	typ string

	lastKey int
	keyStep int
	limit   int

	keyNumIndex int
	firstKey    int
}

func (s *findKeysSpec) Cast(value any) error {
	typ, nested, err := typedSpec(value)
	if err != nil {
		return err
	}

	switch typ {
	case "range":
		lastKey, err := parseField[cast.Int](nested, "lastkey")
		if err != nil {
			return err
		}

		keyStep, err := parseField[cast.Int](nested, "keystep")
		if err != nil {
			return err
		}

		limit, err := parseField[cast.Int](nested, "limit")
		if err != nil {
			return err
		}

		*s = findKeysSpec{
			typ:         typ,
			lastKey:     int(lastKey),
			keyStep:     int(keyStep),
			limit:       int(limit),
			keyNumIndex: 0,
			firstKey:    0,
		}
	case "keynum":
		keyNumIndex, err := parseField[cast.Int](nested, "keynumidx")
		if err != nil {
			return err
		}

		firstKey, err := parseField[cast.Int](nested, "firstkey")
		if err != nil {
			return err
		}

		keyStep, err := parseField[cast.Int](nested, "keystep")
		if err != nil {
			return err
		}

		*s = findKeysSpec{
			typ:         typ,
			lastKey:     0,
			keyStep:     int(keyStep),
			limit:       0,
			keyNumIndex: int(keyNumIndex),
			firstKey:    int(firstKey),
		}
	default:
		return fmt.Errorf("find keys type %q is not supported: %w", typ, errors.ErrUnsupported)
	}

	return nil
}

func (s *findKeysSpec) indexes(args []any, offset int) ([]int, error) {
	switch s.typ {
	case "range":
		var last int

		switch {
		case s.lastKey >= 0:
			if s.lastKey >= len(args)-offset {
				return nil, fmt.Errorf(
					"expected last key offset in range [0, %d], got %d",
					len(args)-offset-1,
					s.lastKey,
				)
			}

			last = offset + s.lastKey
		case s.limit == 0:
			last = len(args) + s.lastKey
		default:
			last = offset + (len(args)-offset)/s.limit - 1
		}

		if offset >= len(args) || last < offset || last >= len(args) {
			return nil, fmt.Errorf(
				"expected key range within argument indexes [0, %d], got [%d, %d]",
				len(args)-1,
				offset,
				last,
			)
		}

		return steps(offset, last, s.keyStep), nil

	case "keynum":
		keyNumIndex := offset + s.keyNumIndex
		if keyNumIndex < 0 || keyNumIndex >= len(args) {
			return nil, fmt.Errorf(
				"expected key count index in range [0, %d], got %d",
				len(args)-1,
				keyNumIndex,
			)
		}

		var count cast.Int
		if err := count.Cast(args[keyNumIndex]); err != nil {
			return nil, fmt.Errorf("failed to cast key count for index %d: %w", keyNumIndex, err)
		}

		numKeys := int(count)
		if numKeys < 0 {
			return nil, fmt.Errorf("expected non-negative key count, got %d", numKeys)
		}

		if numKeys == 0 {
			return nil, nil
		}

		first := offset + s.firstKey
		if first < 0 || first >= len(args) {
			return nil, fmt.Errorf(
				"expected first key index in range [0, %d], got %d",
				len(args)-1,
				first,
			)
		}

		if numKeys-1 > (len(args)-1-first)/s.keyStep {
			maxKeys := (len(args)-1-first)/s.keyStep + 1

			return nil, fmt.Errorf(
				"expected at most %d keys from index %d with step %d, got %d",
				maxKeys,
				first,
				s.keyStep,
				numKeys,
			)
		}

		last := first + (numKeys-1)*s.keyStep

		return steps(first, last, s.keyStep), nil

	default:
		return nil, fmt.Errorf(
			"find keys type %q is not supported: %w",
			s.typ,
			errors.ErrUnsupported,
		)
	}
}

// KeyFinder identifies key arguments in calls to a Redis command.
type KeyFinder struct {
	client      redis.UniversalClient
	subcommands map[string]*KeyFinder
	specs       []spec
	isDynamic   bool
}

// Indexes returns the indexes of all Redis key arguments in args.
func (f *KeyFinder) Indexes(ctx context.Context, args []any) ([]int, error) {
	if len(f.subcommands) > 0 && len(args) > 1 {
		var name cast.String
		if err := name.Cast(args[1]); err != nil {
			return nil, fmt.Errorf("failed to cast subcommand name for argument index 1: %w", err)
		}

		if finder, ok := f.subcommands[strings.ToLower(string(name))]; ok {
			return finder.Indexes(ctx, args)
		}
	}

	if f.isDynamic {
		return f.dynamicIndexes(ctx, args)
	}

	seen := sliceutil.NewSet[int]()

	for i, spec := range f.specs {
		found, err := spec.indexes(args)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to find key indexes for key specification %d: %w",
				i,
				err,
			)
		}

		for _, index := range found {
			seen.Add(index)
		}
	}

	return seen.Values(), nil
}

func (f *KeyFinder) dynamicIndexes(ctx context.Context, args []any) ([]int, error) {
	keys, err := f.client.CommandGetKeys(ctx, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to execute COMMAND GETKEYS: %w", err)
	}

	candidates := make([][]int, len(keys))
	// GETKEYS returns values rather than positions, so probe matching arguments
	// to distinguish keys from non-key arguments that contain the same value.
	for index := 1; index < len(args); index++ {
		var value cast.String
		if err := value.Cast(args[index]); err != nil {
			value = cast.String(fmt.Sprint(args[index]))
		}

		if !slices.Contains(keys, string(value)) {
			continue
		}

		marker := fmt.Sprintf("\x00redistest-key-probe-%d", index)
		for {
			hasCollision := slices.Contains(keys, marker)

			for _, arg := range args {
				var argument cast.String
				if err := argument.Cast(arg); err != nil {
					argument = cast.String(fmt.Sprint(arg))
				}

				if string(argument) == marker {
					hasCollision = true

					break
				}
			}

			if !hasCollision {
				break
			}

			marker += "\x00"
		}

		probeArgs := append([]any(nil), args...)
		probeArgs[index] = marker

		probeKeys, probeErr := f.client.CommandGetKeys(ctx, probeArgs...).Result()
		if probeErr != nil {
			if _, ok := errors.AsType[redis.Error](probeErr); !ok {
				return nil, fmt.Errorf(
					"failed to probe key argument at index %d with COMMAND GETKEYS: %w",
					index,
					probeErr,
				)
			}

			continue
		}

		if len(keys) != len(probeKeys) {
			continue
		}

		isValid := true

		for keyIndex := range keys {
			if keys[keyIndex] != probeKeys[keyIndex] &&
				(keys[keyIndex] != string(value) || probeKeys[keyIndex] != marker) {
				isValid = false

				break
			}
		}

		if !isValid {
			continue
		}

		for keyIndex := range keys {
			if keys[keyIndex] != probeKeys[keyIndex] {
				candidates[keyIndex] = append(candidates[keyIndex], index)
			}
		}
	}

	seen := sliceutil.NewSet[int]()

	for keyIndex, indexes := range candidates {
		if len(indexes) != 1 {
			return nil, fmt.Errorf(
				"failed to resolve key argument %d of %d unambiguously: %w",
				keyIndex+1,
				len(keys),
				errors.ErrUnsupported,
			)
		}

		seen.Add(indexes[0])
	}

	return seen.Values(), nil
}

func parseKeySpec(value any) (*spec, bool, error) {
	var m cast.Map
	if err := m.Cast(value); err != nil {
		return nil, false, fmt.Errorf(
			"failed to cast key specification to map for value %T: %w",
			value,
			err,
		)
	}

	flags, err := parseField[cast.Array](m, "flags")
	if err != nil {
		return nil, false, err
	}

	isIncomplete := false

	for _, value := range flags {
		var flag cast.String
		if err := flag.Cast(value); err != nil {
			return nil, false, fmt.Errorf(
				"failed to cast key specification flag to string for value %T: %w",
				value,
				err,
			)
		}

		switch flag {
		case "not_key":
			return nil, false, nil
		case "incomplete":
			isIncomplete = true
		}
	}

	if isIncomplete {
		return nil, true, nil
	}

	beginSearch, err := parseField[beginSearchSpec](m, "begin_search")
	if err != nil {
		return nil, false, err
	}

	findKeys, err := parseField[findKeysSpec](m, "find_keys")
	if err != nil {
		return nil, false, err
	}

	return &spec{beginSearch: beginSearch, findKeys: findKeys}, false, nil
}

func typedSpec(value any) (string, map[string]any, error) {
	var m cast.Map
	if err := m.Cast(value); err != nil {
		return "", nil, fmt.Errorf(
			"failed to cast typed specification to map for value %T: %w",
			value,
			err,
		)
	}

	typ, err := parseField[cast.String](m, "type")
	if err != nil {
		return "", nil, err
	}

	nested, err := parseField[cast.Map](m, "spec")
	if err != nil {
		return "", nil, err
	}

	return string(typ), nested, nil
}

func parseField[T any, P interface {
	*T
	cast.Caster
}](m map[string]any, name string) (T, error) {
	var result T
	if v, ok := m[name]; ok {
		if err := P(&result).Cast(v); err != nil {
			return result, fmt.Errorf("failed to cast field for %q: %w", name, err)
		}

		return result, nil
	}

	return result, fmt.Errorf("expected field %q, got none", name)
}

func isStringEqualFold(value any, expected string) bool {
	var argument cast.String
	if argument.Cast(value) != nil {
		return false
	}

	return strings.EqualFold(string(argument), expected)
}

func steps(first, last, step int) []int {
	idx := make([]int, 0, (last-first)/step+1)
	for i := first; i <= last; i += step {
		idx = append(idx, i)
	}

	return idx
}
