package protocol

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ErrUnexpectedEOF means an SSE frame ended without its blank-line boundary.
var ErrUnexpectedEOF = errors.New("unexpected EOF inside SSE frame")

// SSEFrame is one complete Server-Sent Events frame.
type SSEFrame struct {
	Event       string
	ID          string
	RetryMillis *int
	Comments    []string
	Data        []byte
}

// SSEDecoder reads complete SSE frames from an input stream.
type SSEDecoder struct {
	reader *bufio.Reader
}

// NewSSEDecoder returns a full-frame SSE decoder.
func NewSSEDecoder(reader io.Reader) *SSEDecoder {
	return &SSEDecoder{reader: bufio.NewReader(reader)}
}

// Next returns the next frame after its blank-line terminator.
func (d *SSEDecoder) Next() (SSEFrame, error) {
	if d == nil || d.reader == nil {
		return SSEFrame{}, fmt.Errorf("decode SSE: nil reader")
	}
	var frame SSEFrame
	var dataLines []string
	sawRecognizedLine := false

	for {
		line, err := d.reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return SSEFrame{}, fmt.Errorf("read SSE line: %w", err)
		}
		if len(line) > 0 {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				if !sawRecognizedLine {
					if errors.Is(err, io.EOF) {
						return SSEFrame{}, io.EOF
					}
					continue
				}
				frame.Data = []byte(strings.Join(dataLines, "\n"))
				return frame, nil
			}
			recognized, parseErr := parseSSELine(&frame, &dataLines, line)
			if parseErr != nil {
				return SSEFrame{}, parseErr
			}
			sawRecognizedLine = sawRecognizedLine || recognized
		}
		if errors.Is(err, io.EOF) {
			if sawRecognizedLine {
				return SSEFrame{}, ErrUnexpectedEOF
			}
			return SSEFrame{}, io.EOF
		}
	}
}

func parseSSELine(frame *SSEFrame, dataLines *[]string, line string) (bool, error) {
	if strings.HasPrefix(line, ":") {
		comment := strings.TrimPrefix(line, ":")
		comment = strings.TrimPrefix(comment, " ")
		frame.Comments = append(frame.Comments, comment)
		return true, nil
	}
	field, value, found := strings.Cut(line, ":")
	if !found {
		value = ""
	}
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "event":
		frame.Event = value
	case "id":
		frame.ID = value
	case "retry":
		retry, err := strconv.Atoi(value)
		if err != nil || retry < 0 {
			return true, fmt.Errorf("decode SSE retry %q: must be a non-negative integer", value)
		}
		frame.RetryMillis = &retry
	case "data":
		*dataLines = append(*dataLines, value)
	default:
		return false, nil
	}
	return true, nil
}

// WriteSSE writes one complete frame and its blank-line terminator.
func WriteSSE(writer io.Writer, frame SSEFrame) error {
	if writer == nil {
		return fmt.Errorf("write SSE: nil writer")
	}
	for _, comment := range frame.Comments {
		if err := writeSSELine(writer, ": "+comment); err != nil {
			return err
		}
	}
	if frame.Event != "" {
		if err := writeSSELine(writer, "event: "+frame.Event); err != nil {
			return err
		}
	}
	if frame.ID != "" {
		if err := writeSSELine(writer, "id: "+frame.ID); err != nil {
			return err
		}
	}
	if frame.RetryMillis != nil {
		if *frame.RetryMillis < 0 {
			return fmt.Errorf("write SSE: retry must be non-negative")
		}
		if err := writeSSELine(writer, "retry: "+strconv.Itoa(*frame.RetryMillis)); err != nil {
			return err
		}
	}
	if frame.Data != nil {
		for _, line := range strings.Split(string(frame.Data), "\n") {
			if err := writeSSELine(writer, "data: "+line); err != nil {
				return err
			}
		}
	}
	if _, err := io.WriteString(writer, "\n"); err != nil {
		return fmt.Errorf("write SSE terminator: %w", err)
	}
	return nil
}

func writeSSELine(writer io.Writer, line string) error {
	if _, err := io.WriteString(writer, line+"\n"); err != nil {
		return fmt.Errorf("write SSE line: %w", err)
	}
	return nil
}
