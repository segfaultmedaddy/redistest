package resp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const (
	status      = '+'
	errorReply  = '-'
	stringReply = '$'
	integer     = ':'
	null        = '_'
	array       = '*'
	mapReply    = '%'
	set         = '~'
)

type Reader struct {
	rd *bufio.Reader
}

func NewReader(rd io.Reader) *Reader {
	return &Reader{rd: bufio.NewReader(rd)}
}

func (r *Reader) ReadReply() (any, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}

	switch line[0] {
	case status:
		return string(line[1:]), nil
	case errorReply:
		return nil, fmt.Errorf("redis: %s", line[1:])
	case stringReply:
		return r.readString(line)
	case integer:
		value, err := strconv.ParseInt(string(line[1:]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse RESP3 integer for value %q: %w", line[1:], err)
		}

		return value, nil
	case null:
		if len(line) != 1 {
			return nil, fmt.Errorf("expected RESP3 null reply %q, got %q", "_", line)
		}

		return nil, nil //nolint:nilnil // A RESP null reply is represented by a nil value and nil error.
	case array, set:
		return r.readArray(line)
	case mapReply:
		return r.readMap(line)
	default:
		return nil, fmt.Errorf("RESP3 reply type %q is not supported: %w", line[0], errors.ErrUnsupported)
	}
}

func (r *Reader) readLine() ([]byte, error) {
	line, err := r.rd.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read RESP3 line for reply: %w", err)
	}

	if len(line) < 3 || line[len(line)-2] != '\r' {
		return nil, fmt.Errorf("expected RESP3 line ending in CRLF, got %q", line)
	}

	return line[:len(line)-2], nil
}

func (r *Reader) readString(line []byte) (string, error) {
	n, err := replyLen(line)
	if err != nil {
		return "", fmt.Errorf("failed to parse RESP3 string length for header %q: %w", line, err)
	}

	b := make([]byte, n+2)
	if _, err := io.ReadFull(r.rd, b); err != nil {
		return "", fmt.Errorf("failed to read RESP3 string for length %d: %w", n, err)
	}

	if b[n] != '\r' || b[n+1] != '\n' {
		return "", fmt.Errorf("expected RESP3 string ending in CRLF, got %q", b[n:])
	}

	return string(b[:n]), nil
}

func (r *Reader) readArray(line []byte) ([]any, error) {
	n, err := replyLen(line)
	if err != nil {
		return nil, fmt.Errorf("failed to parse RESP3 array length for header %q: %w", line, err)
	}

	values := make([]any, n)
	for i := range values {
		values[i], err = r.ReadReply()
		if err != nil {
			return nil, fmt.Errorf("failed to read RESP3 array element for index %d: %w", i, err)
		}
	}

	return values, nil
}

func (r *Reader) readMap(line []byte) (map[string]any, error) {
	n, err := replyLen(line)
	if err != nil {
		return nil, fmt.Errorf("failed to parse RESP3 map length for header %q: %w", line, err)
	}

	values := make(map[string]any, n)
	for i := range n {
		key, err := r.ReadReply()
		if err != nil {
			return nil, fmt.Errorf("failed to read RESP3 map key for index %d: %w", i, err)
		}

		keyString, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("expected string map key, got %T", key)
		}

		values[keyString], err = r.ReadReply()
		if err != nil {
			return nil, fmt.Errorf("failed to read RESP3 map value for key %q: %w", keyString, err)
		}
	}

	return values, nil
}

func replyLen(line []byte) (int, error) {
	n, err := strconv.Atoi(string(line[1:]))
	if err != nil {
		return 0, fmt.Errorf("failed to parse RESP3 reply length for value %q: %w", line[1:], err)
	}

	if n < 0 {
		return 0, fmt.Errorf("expected non-negative RESP3 reply length, got %d", n)
	}

	return n, nil
}
