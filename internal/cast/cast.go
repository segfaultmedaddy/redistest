// Package cast converts dynamically typed RESP values into the concrete types
// used while parsing Redis command metadata.
package cast

import (
	"fmt"
	"strconv"
)

// Caster converts a dynamically typed value into its receiver.
type Caster interface {
	Cast(any) error
}

// Int is a native-sized signed integer cast target.
type Int int

// Cast converts integer types and base-10 strings or byte slices to Int. It
// returns an error when the value is not an integer or cannot fit in an int.
func (target *Int) Cast(value any) error {
	var (
		result int
		err    error
	)

	switch value := value.(type) {
	case string:
		var n int64

		n, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			err = fmt.Errorf("failed to parse integer for value %q: %w", value, err)
		} else {
			result, err = signedInt(n)
		}
	case []byte:
		var n int64

		n, err = strconv.ParseInt(string(value), 10, 64)
		if err != nil {
			err = fmt.Errorf("failed to parse integer for value %q: %w", value, err)
		} else {
			result, err = signedInt(n)
		}
	case int:
		result = value
	case int8:
		result = int(value)
	case int16:
		result = int(value)
	case int32:
		result = int(value)
	case int64:
		result, err = signedInt(value)
	case uint:
		result, err = unsignedInt(uint64(value))
	case uint8:
		result = int(value)
	case uint16:
		result = int(value)
	case uint32:
		result, err = unsignedInt(uint64(value))
	case uint64:
		result, err = unsignedInt(value)
	default:
		err = fmt.Errorf("expected an integer, got %T", value)
	}

	if err != nil {
		return err
	}

	*target = Int(result)

	return nil
}

// String is a string cast target.
type String string

// Cast converts a string or byte slice to String.
func (target *String) Cast(value any) error {
	switch value := value.(type) {
	case string:
		*target = String(value)
	case []byte:
		*target = String(value)
	default:
		return fmt.Errorf("expected a string, got %T", value)
	}

	return nil
}

// Array is an array cast target for decoded RESP arrays.
type Array []any

// Cast converts a []any value to Array.
func (target *Array) Cast(value any) error {
	array, ok := value.([]any)
	if !ok {
		return fmt.Errorf("expected an array, got %T", value)
	}

	*target = array

	return nil
}

// Map is a map cast target for decoded RESP maps.
type Map map[string]any

// Cast converts a map[string]any value to Map.
func (target *Map) Cast(value any) error {
	m, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("expected a map, got %T", value)
	}

	*target = m

	return nil
}

func signedInt(value int64) (int, error) {
	result := int(value)
	if int64(result) != value {
		return 0, fmt.Errorf("expected integer representable as int, got %d", value)
	}

	return result, nil
}

func unsignedInt(value uint64) (int, error) {
	result := int(value) //nolint:gosec // The round-trip check below rejects overflowing conversions.
	if result < 0 || uint64(result) != value {
		return 0, fmt.Errorf("expected integer representable as int, got %d", value)
	}

	return result, nil
}
