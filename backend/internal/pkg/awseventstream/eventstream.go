// Package awseventstream decodes the AWS event-stream wire format.
package awseventstream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	PreludeSize           = 12
	MinimumMessageSize    = PreludeSize + 4
	DefaultMaxMessageSize = 16 * 1024 * 1024
)

type HeaderType byte

const (
	HeaderBoolTrue HeaderType = iota
	HeaderBoolFalse
	HeaderByte
	HeaderShort
	HeaderInteger
	HeaderLong
	HeaderByteArray
	HeaderString
	HeaderTimestamp
	HeaderUUID
)

// HeaderValue contains one of the AWS event-stream header value types. Value is
// bool, int8, int16, int32, int64, []byte, string, or [16]byte respectively.
type HeaderValue struct {
	Type  HeaderType
	Value any
}

func (v HeaderValue) String() (string, bool) {
	s, ok := v.Value.(string)
	return s, ok
}

type Headers map[string]HeaderValue

func (h Headers) String(name string) (string, bool) {
	v, ok := h[name]
	if !ok {
		return "", false
	}
	return v.String()
}

func (h Headers) MessageType() string   { v, _ := h.String(":message-type"); return v }
func (h Headers) EventType() string     { v, _ := h.String(":event-type"); return v }
func (h Headers) ExceptionType() string { v, _ := h.String(":exception-type"); return v }
func (h Headers) ErrorCode() string     { v, _ := h.String(":error-code"); return v }

type Message struct {
	Headers Headers
	Payload []byte
}

// MessageError represents an AWS event-stream error or exception message.
type MessageError struct {
	MessageType string
	Code        string
	Payload     []byte
}

func (e *MessageError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("aws event-stream %s: %s", e.MessageType, e.Payload)
	}
	return fmt.Sprintf("aws event-stream %s %s: %s", e.MessageType, e.Code, e.Payload)
}

// Error returns a typed error for error/exception messages and nil for events.
func (m *Message) Error() error {
	kind := m.Headers.MessageType()
	if kind != "error" && kind != "exception" {
		return nil
	}
	code := m.Headers.ErrorCode()
	if kind == "exception" {
		code = m.Headers.ExceptionType()
	}
	return &MessageError{MessageType: kind, Code: code, Payload: append([]byte(nil), m.Payload...)}
}

type ErrorKind string

const (
	ErrorInvalidLength   ErrorKind = "invalid_length"
	ErrorMessageTooLarge ErrorKind = "message_too_large"
	ErrorPreludeCRC      ErrorKind = "prelude_crc"
	ErrorMessageCRC      ErrorKind = "message_crc"
	ErrorHeaders         ErrorKind = "headers"
)

type DecodeError struct {
	Kind    ErrorKind
	Message string
}

func (e *DecodeError) Error() string { return "aws event-stream: " + e.Message }

func decodeError(kind ErrorKind, format string, args ...any) error {
	return &DecodeError{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

type Reader struct {
	r              io.Reader
	maxMessageSize uint32
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: r, maxMessageSize: DefaultMaxMessageSize}
}

func NewReaderSize(r io.Reader, maxMessageSize uint32) *Reader {
	if maxMessageSize < MinimumMessageSize {
		maxMessageSize = MinimumMessageSize
	}
	return &Reader{r: r, maxMessageSize: maxMessageSize}
}

// ReadMessage reads exactly one frame. It does not depend on the chunking of r.
func (r *Reader) ReadMessage() (*Message, error) {
	prelude := make([]byte, PreludeSize)
	n, err := io.ReadFull(r.r, prelude)
	if err != nil {
		if errors.Is(err, io.EOF) && n == 0 {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("aws event-stream prelude: %w", err)
	}

	total := binary.BigEndian.Uint32(prelude[:4])
	headersLength := binary.BigEndian.Uint32(prelude[4:8])
	if total < MinimumMessageSize {
		return nil, decodeError(ErrorInvalidLength, "message length %d is smaller than %d", total, MinimumMessageSize)
	}
	if total > r.maxMessageSize {
		return nil, decodeError(ErrorMessageTooLarge, "message length %d exceeds limit %d", total, r.maxMessageSize)
	}
	if headersLength > total-MinimumMessageSize {
		return nil, decodeError(ErrorInvalidLength, "headers length %d exceeds message payload boundary", headersLength)
	}
	wantPreludeCRC := binary.BigEndian.Uint32(prelude[8:12])
	gotPreludeCRC := crc32.ChecksumIEEE(prelude[:8])
	if gotPreludeCRC != wantPreludeCRC {
		return nil, decodeError(ErrorPreludeCRC, "prelude CRC mismatch: expected 0x%08x, got 0x%08x", wantPreludeCRC, gotPreludeCRC)
	}

	rest := make([]byte, int(total)-PreludeSize)
	if _, err := io.ReadFull(r.r, rest); err != nil {
		return nil, fmt.Errorf("aws event-stream message: %w", err)
	}
	wantMessageCRC := binary.BigEndian.Uint32(rest[len(rest)-4:])
	h := crc32.NewIEEE()
	_, _ = h.Write(prelude)
	_, _ = h.Write(rest[:len(rest)-4])
	if got := h.Sum32(); got != wantMessageCRC {
		return nil, decodeError(ErrorMessageCRC, "message CRC mismatch: expected 0x%08x, got 0x%08x", wantMessageCRC, got)
	}

	headers, err := ParseHeaders(rest[:headersLength])
	if err != nil {
		return nil, err
	}
	payload := append([]byte(nil), rest[headersLength:len(rest)-4]...)
	return &Message{Headers: headers, Payload: payload}, nil
}

func ParseHeaders(data []byte) (Headers, error) {
	h := make(Headers)
	for pos := 0; pos < len(data); {
		nameLength := int(data[pos])
		pos++
		if nameLength == 0 {
			return nil, decodeError(ErrorHeaders, "header name is empty")
		}
		if len(data)-pos < nameLength+1 {
			return nil, decodeError(ErrorHeaders, "truncated header name or type")
		}
		name := string(data[pos : pos+nameLength])
		pos += nameLength
		typ := HeaderType(data[pos])
		pos++

		var value any
		switch typ {
		case HeaderBoolTrue:
			value = true
		case HeaderBoolFalse:
			value = false
		case HeaderByte:
			if len(data)-pos < 1 {
				return nil, truncatedHeader(name)
			}
			value = int8(data[pos])
			pos++
		case HeaderShort:
			if len(data)-pos < 2 {
				return nil, truncatedHeader(name)
			}
			value = int16(binary.BigEndian.Uint16(data[pos:]))
			pos += 2
		case HeaderInteger:
			if len(data)-pos < 4 {
				return nil, truncatedHeader(name)
			}
			value = int32(binary.BigEndian.Uint32(data[pos:]))
			pos += 4
		case HeaderLong, HeaderTimestamp:
			if len(data)-pos < 8 {
				return nil, truncatedHeader(name)
			}
			value = int64(binary.BigEndian.Uint64(data[pos:]))
			pos += 8
		case HeaderByteArray, HeaderString:
			if len(data)-pos < 2 {
				return nil, truncatedHeader(name)
			}
			length := int(binary.BigEndian.Uint16(data[pos:]))
			pos += 2
			if len(data)-pos < length {
				return nil, truncatedHeader(name)
			}
			if typ == HeaderString {
				value = string(data[pos : pos+length])
			} else {
				value = append([]byte(nil), data[pos:pos+length]...)
			}
			pos += length
		case HeaderUUID:
			if len(data)-pos < 16 {
				return nil, truncatedHeader(name)
			}
			var uuid [16]byte
			copy(uuid[:], data[pos:pos+16])
			pos += 16
			value = uuid
		default:
			return nil, decodeError(ErrorHeaders, "header %q has unknown type %d", name, typ)
		}
		h[name] = HeaderValue{Type: typ, Value: value}
	}
	return h, nil
}

func truncatedHeader(name string) error {
	return decodeError(ErrorHeaders, "header %q is truncated", name)
}
