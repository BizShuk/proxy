package protocol

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSEDecoderMultilineData(t *testing.T) {
	raw := "event: response.output_text.delta\r\nid: event_1\r\nretry: 1500\r\n: keepalive\r\ndata: {\"type\":\r\ndata: \"delta\"}\r\n\r\n"
	frame, err := NewSSEDecoder(strings.NewReader(raw)).Next()
	require.NoError(t, err)
	assert.Equal(t, "response.output_text.delta", frame.Event)
	assert.Equal(t, "event_1", frame.ID)
	require.NotNil(t, frame.RetryMillis)
	assert.Equal(t, 1500, *frame.RetryMillis)
	assert.Equal(t, []string{"keepalive"}, frame.Comments)
	assert.Equal(t, "{\"type\":\n\"delta\"}", string(frame.Data))
}

func TestSSEDecoderRejectsPartialFrameAtEOF(t *testing.T) {
	_, err := NewSSEDecoder(strings.NewReader("event: message_start\ndata: {}\n")).Next()
	require.ErrorIs(t, err, ErrUnexpectedEOF)
}

func TestSSEDecoderReturnsEOFWhenEmpty(t *testing.T) {
	_, err := NewSSEDecoder(strings.NewReader("")).Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestSSEDecoderRejectsLineOverLimit(t *testing.T) {
	raw := "data: " + strings.Repeat("x", 32) + "\n\n"
	_, err := NewBoundedSSEDecoder(strings.NewReader(raw), 20).Next()
	require.ErrorIs(t, err, ErrSSELineTooLarge)
}

func TestSSEDecoderRejectsFrameOverLimit(t *testing.T) {
	raw := "data: 12345\ndata: 67890\n\n"
	_, err := NewBoundedSSEDecoder(strings.NewReader(raw), 20).Next()
	require.ErrorIs(t, err, ErrSSEFrameTooLarge)
}

func TestWriteSSERoundTrip(t *testing.T) {
	retry := 250
	want := SSEFrame{
		Event: "message_stop", ID: "event_2", RetryMillis: &retry,
		Comments: []string{"one", "two"}, Data: []byte("first\nsecond"),
	}
	var out bytes.Buffer
	require.NoError(t, WriteSSE(&out, want))

	got, err := NewSSEDecoder(&out).Next()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestWriteSSEPropagatesWriterError(t *testing.T) {
	errBoom := errors.New("boom")
	err := WriteSSE(errorWriter{err: errBoom}, SSEFrame{Data: []byte("data")})
	require.ErrorIs(t, err, errBoom)
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }
