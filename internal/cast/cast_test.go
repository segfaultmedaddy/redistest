package cast_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.segfaultmedaddy.com/redistest/internal/cast"
)

func Test_Int_Cast(t *testing.T) {
	t.Parallel()

	validTests := []struct {
		value         any
		name          string
		expectedValue cast.Int
	}{
		{name: "should cast a decimal string when it represents an integer", value: "-42", expectedValue: -42},
		{
			name:          "should cast a byte slice when it represents an integer",
			value:         []byte("42"),
			expectedValue: 42,
		},
		{name: "should cast an int when it contains an integer", value: int(-42), expectedValue: -42},
		{name: "should cast an int8 when it contains an integer", value: int8(-42), expectedValue: -42},
		{name: "should cast an int16 when it contains an integer", value: int16(-42), expectedValue: -42},
		{name: "should cast an int32 when it contains an integer", value: int32(-42), expectedValue: -42},
		{name: "should cast an int64 when it fits in an int", value: int64(-42), expectedValue: -42},
		{name: "should cast a uint when it fits in an int", value: uint(42), expectedValue: 42},
		{name: "should cast a uint8 when it contains an integer", value: uint8(42), expectedValue: 42},
		{name: "should cast a uint16 when it contains an integer", value: uint16(42), expectedValue: 42},
		{name: "should cast a uint32 when it fits in an int", value: uint32(42), expectedValue: 42},
		{name: "should cast a uint64 when it fits in an int", value: uint64(42), expectedValue: 42},
	}

	for _, tt := range validTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			expectedValue := tt.expectedValue
			actualValue := cast.Int(17)

			// Act
			err := actualValue.Cast(tt.value)
			require.NoError(t, err)

			// Assert
			assert.Equal(t, expectedValue, actualValue)
		})
	}

	invalidTests := []struct {
		name          string
		value         any
		expectedError string
	}{
		{
			name:          "should return an error when a string is not a decimal integer",
			value:         "not-an-integer",
			expectedError: "failed to parse integer",
		},
		{
			name:          "should return an error when a byte slice is not a decimal integer",
			value:         []byte("not-an-integer"),
			expectedError: "failed to parse integer",
		},
		{
			name:          "should return an error when the value has a non-integer type",
			value:         42.0,
			expectedError: "expected an integer",
		},
		{
			name:          "should return an error when a uint exceeds the maximum int",
			value:         ^uint(0),
			expectedError: "expected integer representable as int",
		},
		{
			name:          "should return an error when a uint64 exceeds the maximum int",
			value:         ^uint64(0),
			expectedError: "expected integer representable as int",
		},
	}

	if strconv.IntSize < 64 {
		invalidTests = append(invalidTests, struct {
			name          string
			value         any
			expectedError string
		}{
			name:          "should return an error when an int64 exceeds the maximum int",
			value:         int64(1 << 31),
			expectedError: "expected integer representable as int",
		})
	}

	for _, tt := range invalidTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			expectedValue := cast.Int(17)
			expectedError := tt.expectedError
			actualValue := expectedValue

			// Act
			actualError := actualValue.Cast(tt.value)

			// Assert
			require.ErrorContains(t, actualError, expectedError)
			assert.Equal(t, expectedValue, actualValue)
		})
	}
}

func Test_String_Cast(t *testing.T) {
	t.Parallel()

	validTests := []struct {
		name          string
		value         any
		expectedValue cast.String
	}{
		{
			name:          "should cast a string when the value is a string",
			value:         "value",
			expectedValue: "value",
		},
		{
			name:          "should cast a byte slice when the value contains bytes",
			value:         []byte("value"),
			expectedValue: "value",
		},
	}

	for _, tt := range validTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			expectedValue := tt.expectedValue
			actualValue := cast.String("initial")

			// Act
			err := actualValue.Cast(tt.value)
			require.NoError(t, err)

			// Assert
			assert.Equal(t, expectedValue, actualValue)
		})
	}

	t.Run("should return an error when the value is not string data", func(t *testing.T) {
		t.Parallel()

		// Arrange
		expectedValue := cast.String("initial")
		expectedError := "expected a string"
		actualValue := expectedValue

		// Act
		actualError := actualValue.Cast(42)

		// Assert
		require.ErrorContains(t, actualError, expectedError)
		assert.Equal(t, expectedValue, actualValue)
	})
}

func Test_Array_Cast(t *testing.T) {
	t.Parallel()

	validTests := []struct {
		name          string
		value         []any
		expectedValue cast.Array
	}{
		{
			name:          "should cast an array when the value contains elements",
			value:         []any{"value", int64(42)},
			expectedValue: cast.Array{"value", int64(42)},
		},
		{
			name:          "should cast an array when the value is nil",
			value:         nil,
			expectedValue: nil,
		},
	}

	for _, tt := range validTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			expectedValue := tt.expectedValue
			actualValue := cast.Array{"initial"}

			// Act
			err := actualValue.Cast(tt.value)
			require.NoError(t, err)

			// Assert
			assert.Equal(t, expectedValue, actualValue)
		})
	}

	t.Run("should return an error when the value is not an any array", func(t *testing.T) {
		t.Parallel()

		// Arrange
		expectedValue := cast.Array{"initial"}
		expectedError := "expected an array"
		actualValue := expectedValue

		// Act
		actualError := actualValue.Cast([]string{"value"})

		// Assert
		require.ErrorContains(t, actualError, expectedError)
		assert.Equal(t, expectedValue, actualValue)
	})
}

func Test_Map_Cast(t *testing.T) {
	t.Parallel()

	validTests := []struct {
		value         map[string]any
		expectedValue cast.Map
		name          string
	}{
		{
			name:          "should cast a map when the value contains entries",
			value:         map[string]any{"key": "value"},
			expectedValue: cast.Map{"key": "value"},
		},
		{
			name:          "should cast a map when the value is nil",
			value:         nil,
			expectedValue: nil,
		},
	}

	for _, tt := range validTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			expectedValue := tt.expectedValue
			actualValue := cast.Map{"initial": "value"}

			// Act
			err := actualValue.Cast(tt.value)
			require.NoError(t, err)

			// Assert
			assert.Equal(t, expectedValue, actualValue)
		})
	}

	t.Run("should return an error when the value is not an any map", func(t *testing.T) {
		t.Parallel()

		// Arrange
		expectedValue := cast.Map{"initial": "value"}
		expectedError := "expected a map"
		actualValue := expectedValue

		// Act
		actualError := actualValue.Cast(map[string]string{"key": "value"})

		// Assert
		require.ErrorContains(t, actualError, expectedError)
		assert.Equal(t, expectedValue, actualValue)
	})
}
