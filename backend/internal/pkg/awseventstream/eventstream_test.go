package awseventstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"testing"
)

func encodeStringHeader(name, value string) []byte {
	var b bytes.Buffer
	_, _ = b.Write([]byte{byte(len(name))})
	_, _ = b.WriteString(name)
	_, _ = b.Write([]byte{byte(HeaderString)})
	_ = binary.Write(&b, binary.BigEndian, uint16(len(value)))
	_, _ = b.WriteString(value)
	return b.Bytes()
}

func encodeFrame(headers, payload []byte) []byte {
	total := uint32(PreludeSize + len(headers) + len(payload) + 4)
	frame := make([]byte, total)
	binary.BigEndian.PutUint32(frame[:4], total)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(headers)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
	copy(frame[12:], headers)
	copy(frame[12+len(headers):], payload)
	binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
	return frame
}

type fragmentReader struct {
	data []byte
	n    int
}

func (r *fragmentReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.n
	if n > len(r.data) {
		n = len(r.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestReaderCRCTruncationFragmentationAndLimits(t *testing.T) {
	headers := append(encodeStringHeader(":message-type", "event"), encodeStringHeader(":event-type", "chunk")...)
	frame := encodeFrame(headers, []byte(`{"ok":true}`))

	t.Run("fragmented reader", func(t *testing.T) {
		m, err := NewReader(&fragmentReader{data: frame, n: 1}).ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Headers.EventType(); got != "chunk" {
			t.Fatalf("event type = %q", got)
		}
		if got := string(m.Payload); got != `{"ok":true}` {
			t.Fatalf("payload = %q", got)
		}
	})

	t.Run("prelude CRC", func(t *testing.T) {
		bad := append([]byte(nil), frame...)
		bad[8] ^= 1
		_, err := NewReader(bytes.NewReader(bad)).ReadMessage()
		var de *DecodeError
		if !errors.As(err, &de) || de.Kind != ErrorPreludeCRC {
			t.Fatalf("error = %#v", err)
		}
	})

	t.Run("message CRC", func(t *testing.T) {
		bad := append([]byte(nil), frame...)
		bad[len(bad)-1] ^= 1
		_, err := NewReader(bytes.NewReader(bad)).ReadMessage()
		var de *DecodeError
		if !errors.As(err, &de) || de.Kind != ErrorMessageCRC {
			t.Fatalf("error = %#v", err)
		}
	})

	t.Run("truncated frame", func(t *testing.T) {
		_, err := NewReader(bytes.NewReader(frame[:len(frame)-2])).ReadMessage()
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("oversize rejected before allocation", func(t *testing.T) {
		prelude := make([]byte, PreludeSize)
		binary.BigEndian.PutUint32(prelude[:4], 1025)
		binary.BigEndian.PutUint32(prelude[8:], crc32.ChecksumIEEE(prelude[:8]))
		_, err := NewReaderSize(bytes.NewReader(prelude), 1024).ReadMessage()
		var de *DecodeError
		if !errors.As(err, &de) || de.Kind != ErrorMessageTooLarge {
			t.Fatalf("error = %#v", err)
		}
	})
}

func TestParseTypedHeaders(t *testing.T) {
	var data bytes.Buffer
	write := func(name string, typ HeaderType, raw []byte) {
		_, _ = data.Write([]byte{byte(len(name))})
		_, _ = data.WriteString(name)
		_, _ = data.Write([]byte{byte(typ)})
		_, _ = data.Write(raw)
	}
	write("t", HeaderBoolTrue, nil)
	write("f", HeaderBoolFalse, nil)
	write("b", HeaderByte, []byte{0xfe})
	var n [8]byte
	shortValue := int16(-123)
	binary.BigEndian.PutUint16(n[:2], uint16(shortValue))
	write("s", HeaderShort, n[:2])
	integerValue := int32(-456)
	binary.BigEndian.PutUint32(n[:4], uint32(integerValue))
	write("i", HeaderInteger, n[:4])
	binary.BigEndian.PutUint64(n[:], uint64(789))
	write("l", HeaderLong, n[:])
	write("a", HeaderByteArray, []byte{0, 2, 1, 2})
	write("x", HeaderString, []byte{0, 2, 'o', 'k'})
	binary.BigEndian.PutUint64(n[:], uint64(1234))
	write("z", HeaderTimestamp, n[:])
	write("u", HeaderUUID, []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})

	h, err := ParseHeaders(data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]any{"t": true, "f": false, "b": int8(-2), "s": int16(-123), "i": int32(-456), "l": int64(789), "x": "ok", "z": int64(1234)}
	for name, want := range checks {
		if got := h[name].Value; got != want {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
	if got, ok := h["a"].Value.([]byte); !ok || !bytes.Equal(got, []byte{1, 2}) {
		t.Errorf("byte array = %v", got)
	}
	if got, ok := h["u"].Value.([16]byte); !ok || got[15] != 15 {
		t.Errorf("uuid = %v", got)
	}
}

func TestMessageError(t *testing.T) {
	m := Message{Headers: Headers{
		":message-type":   {Type: HeaderString, Value: "exception"},
		":exception-type": {Type: HeaderString, Value: "BadThing"},
	}, Payload: []byte(`{"message":"bad"}`)}
	var me *MessageError
	if !errors.As(m.Error(), &me) || me.Code != "BadThing" {
		t.Fatalf("error = %#v", m.Error())
	}
}
