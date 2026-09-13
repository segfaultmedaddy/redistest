package resp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.segfaultmedaddy.com/redistest/internal/resp"
)

func Test_Reader_ReadReply(t *testing.T) {
	t.Parallel()

	validTests := []struct {
		expectedValue any
		input         string
		name          string
	}{
		{
			name:          "should read a status reply when the value is a simple string",
			input:         "+OK\r\n",
			expectedValue: "OK",
		},
		{
			name:          "should read a blob string when the payload contains CRLF",
			input:         "$11\r\nline\r\nbreak\r\n",
			expectedValue: "line\r\nbreak",
		},
		{
			name:          "should read an empty blob string when the length is zero",
			input:         "$0\r\n\r\n",
			expectedValue: "",
		},
		{
			name:          "should read an integer reply when the value is negative",
			input:         ":-42\r\n",
			expectedValue: int64(-42),
		},
		{
			name:          "should read a nil value when the reply is null",
			input:         "_\r\n",
			expectedValue: nil,
		},
		{
			name:          "should read an empty array when the length is zero",
			input:         "*0\r\n",
			expectedValue: []any{},
		},
		{
			name:          "should read each array element when the reply contains mixed values",
			input:         "*4\r\n+OK\r\n:42\r\n$5\r\nvalue\r\n_\r\n",
			expectedValue: []any{"OK", int64(42), "value", nil},
		},
		{
			name:          "should read a set as an array when the reply contains distinct values",
			input:         "~2\r\n+one\r\n+two\r\n",
			expectedValue: []any{"one", "two"},
		},
		{
			name:          "should read a map when the reply contains string keys",
			input:         "%2\r\n+status\r\n+OK\r\n+count\r\n:2\r\n",
			expectedValue: map[string]any{"status": "OK", "count": int64(2)},
		},
		{
			name:          "should read nested aggregates when collections contain collections",
			input:         "*1\r\n%1\r\n+values\r\n~2\r\n:1\r\n:2\r\n",
			expectedValue: []any{map[string]any{"values": []any{int64(1), int64(2)}}},
		},
	}

	for _, tt := range validTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			reader := resp.NewReader(strings.NewReader(tt.input))
			expectedValue := tt.expectedValue

			// Act
			actualValue, err := reader.ReadReply()
			require.NoError(t, err)

			// Assert
			assert.Equal(t, expectedValue, actualValue)
		})
	}

	invalidTests := []struct {
		input         string
		name          string
		expectedError string
	}{
		{
			name:          "should return an error when the input is empty",
			input:         "",
			expectedError: "failed to read RESP3 line for reply",
		},
		{
			name:          "should return an error when a line does not end in CRLF",
			input:         "+OK\n",
			expectedError: "expected RESP3 line ending in CRLF",
		},
		{
			name:          "should return an error when an integer is malformed",
			input:         ":not-an-integer\r\n",
			expectedError: "failed to parse RESP3 integer",
		},
		{
			name:          "should return an error when a null reply contains a value",
			input:         "_value\r\n",
			expectedError: "expected RESP3 null reply",
		},
		{
			name:          "should return a Redis error when the reply is an error",
			input:         "-ERR operation failed\r\n",
			expectedError: "redis: ERR operation failed",
		},
		{
			name:          "should return an error when a blob string length is malformed",
			input:         "$invalid\r\n",
			expectedError: "failed to parse RESP3 string length",
		},
		{
			name:          "should return an error when a blob string length is negative",
			input:         "$-1\r\n",
			expectedError: "expected non-negative RESP3 reply length",
		},
		{
			name:          "should return an error when a blob string payload is truncated",
			input:         "$5\r\nvalue",
			expectedError: "failed to read RESP3 string for length 5",
		},
		{
			name:          "should return an error when a blob string does not end in CRLF",
			input:         "$5\r\nvalueXX",
			expectedError: "expected RESP3 string ending in CRLF",
		},
		{
			name:          "should return an error when an array length is malformed",
			input:         "*invalid\r\n",
			expectedError: "failed to parse RESP3 array length",
		},
		{
			name:          "should return an error when an array element is malformed",
			input:         "*1\r\n:invalid\r\n",
			expectedError: "failed to read RESP3 array element for index 0",
		},
		{
			name:          "should return an error when a map length is malformed",
			input:         "%invalid\r\n",
			expectedError: "failed to parse RESP3 map length",
		},
		{
			name:          "should return an error when a map key is not a string",
			input:         "%1\r\n:1\r\n+value\r\n",
			expectedError: "expected string map key",
		},
		{
			name:          "should return an error when a map key is malformed",
			input:         "%1\r\n:invalid\r\n+value\r\n",
			expectedError: "failed to read RESP3 map key for index 0",
		},
		{
			name:          "should return an error when a map value is missing",
			input:         "%1\r\n+key\r\n",
			expectedError: "failed to read RESP3 map value for key \"key\"",
		},
	}

	for _, tt := range invalidTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			reader := resp.NewReader(strings.NewReader(tt.input))
			expectedError := tt.expectedError

			// Act
			_, actualError := reader.ReadReply()

			// Assert
			require.ErrorContains(t, actualError, expectedError)
		})
	}

	t.Run("should return an unsupported error when the reply type is unknown", func(t *testing.T) {
		t.Parallel()

		// Arrange
		reader := resp.NewReader(strings.NewReader(",1.5\r\n"))
		expectedError := errors.ErrUnsupported

		// Act
		_, actualError := reader.ReadReply()

		// Assert
		require.ErrorIs(t, actualError, expectedError)
	})

	t.Run("should read consecutive replies when the reader has more data", func(t *testing.T) {
		t.Parallel()

		// Arrange
		reader := resp.NewReader(strings.NewReader("+OK\r\n:42\r\n"))
		expectedFirstValue := "OK"
		expectedSecondValue := int64(42)

		// Act
		actualFirstValue, firstErr := reader.ReadReply()
		require.NoError(t, firstErr)

		actualSecondValue, secondErr := reader.ReadReply()
		require.NoError(t, secondErr)

		// Assert
		assert.Equal(t, expectedFirstValue, actualFirstValue)
		assert.Equal(t, expectedSecondValue, actualSecondValue)
	})
}
