//go:build unix

package runner

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Control protocol: a client connects to the runner socket and sends one
// JSON request line. The runner answers with one JSON response line. For
// "attach", the connection then switches to binary frames.

type request struct {
	Op      string `json:"op"` // status | wait | stop | signal | attach
	GraceMS int64  `json:"grace_ms,omitempty"`
	Signal  string `json:"signal,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

type response struct {
	OK     bool    `json:"ok"`
	Error  string  `json:"error,omitempty"`
	Status *Status `json:"status,omitempty"`
	TTY    bool    `json:"tty,omitempty"`
	// Stdin reports whether the attached process accepts input.
	Stdin bool `json:"stdin,omitempty"`
}

// Frame types used after an attach handshake.
const (
	frameOutput byte = 'o' // runner → client: raw output bytes
	frameExit   byte = 'x' // runner → client: final Status JSON
	frameInput  byte = 'i' // client → runner: input bytes
	frameResize byte = 'r' // client → runner: cols, rows (uint32 each)
	frameEOF    byte = 'e' // client → runner: close the child's stdin
)

const maxFrame = 1 << 20

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("runner: frame too large (%d bytes)", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

func encodeResize(cols, rows int) []byte {
	var b [8]byte
	binary.BigEndian.PutUint32(b[:4], uint32(cols))
	binary.BigEndian.PutUint32(b[4:], uint32(rows))
	return b[:]
}

func decodeResize(b []byte) (int, int, error) {
	if len(b) != 8 {
		return 0, 0, errors.New("runner: bad resize frame")
	}
	return int(binary.BigEndian.Uint32(b[:4])), int(binary.BigEndian.Uint32(b[4:])), nil
}

func writeJSONLine(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
