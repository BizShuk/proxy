package model

import (
	"io"

	"github.com/bizshuk/agentsdk/provider/protocol/sse"
)

// ErrUnexpectedEOF means an SSE frame ended without its blank-line boundary.
var ErrUnexpectedEOF = sse.ErrUnexpectedEOF

// ErrSSELineTooLarge means one SSE line exceeded the configured byte limit.
var ErrSSELineTooLarge = sse.ErrLineTooLarge

// ErrSSEFrameTooLarge means one SSE frame exceeded the configured byte limit.
var ErrSSEFrameTooLarge = sse.ErrFrameTooLarge

// MAX_SSE_FRAME_BYTES is the compatibility name for the shared default limit.
const MAX_SSE_FRAME_BYTES = sse.MAX_FRAME_BYTES

// SSEFrame is one complete Server-Sent Events frame.
type SSEFrame = sse.Frame

// SSEDecoder reads complete SSE frames from an input stream.
type SSEDecoder = sse.Decoder

// NewSSEDecoder returns a full-frame SSE decoder.
func NewSSEDecoder(reader io.Reader) *SSEDecoder {
	return sse.NewDecoder(reader)
}

// NewBoundedSSEDecoder returns an SSE decoder with per-line and per-frame limits.
func NewBoundedSSEDecoder(reader io.Reader, maxBytes int64) *SSEDecoder {
	return sse.NewBoundedDecoder(reader, maxBytes)
}

// WriteSSE writes one complete frame and its blank-line terminator.
func WriteSSE(writer io.Writer, frame SSEFrame) error {
	return sse.Write(writer, frame)
}
