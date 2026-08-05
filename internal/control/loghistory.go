package control

import (
	"bytes"
	"io"
	"os"
)

// MaxLogContentBytes caps how much trailing log history is loaded into a single
// log_content frame, even when Tail is 0 ("all"). It stays well under
// protocol.FrameMaxLen so the JSON envelope cannot blow the frame limit.
const MaxLogContentBytes = 8 << 20 // 8 MiB

// ReadLogHistory returns trailing content from f for a logs response.
//
// If lines > 0, at most that many complete lines are returned (from the end).
// Regardless, at most MaxLogContentBytes of trailing content is considered.
// On success, f is positioned at EOF so a follow loop can continue from there.
func ReadLogHistory(f *os.File, lines int) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}

	window := size
	if window > MaxLogContentBytes {
		window = MaxLogContentBytes
	}
	start := size - window

	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	// If we started mid-file, drop the incomplete first line so clients never
	// see a torn timestamp/prefix.
	if start > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}

	if lines > 0 && len(data) > 0 {
		data = lastNLines(data, lines)
	}

	// Follow continues from EOF; ReadAll already left us there, but be explicit
	// in case a future change leaves the offset elsewhere.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return nil, err
	}
	return data, nil
}

// lastNLines returns the trailing n newline-delimited lines of data. A final
// incomplete line (no trailing newline) counts as a line.
func lastNLines(data []byte, n int) []byte {
	if n <= 0 || len(data) == 0 {
		return data
	}
	// Trim a trailing newline so the final empty segment isn't counted as a line.
	trimmed := data
	if trimmed[len(trimmed)-1] == '\n' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	end := len(trimmed)
	for i := 0; i < n; i++ {
		if end <= 0 {
			return data
		}
		j := bytes.LastIndexByte(trimmed[:end], '\n')
		if j < 0 {
			return data
		}
		end = j
	}
	return data[end+1:]
}
