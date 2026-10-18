package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
)

// Sums maps a file path to the content hash of what was read from it: ""
// when the file does not exist, "unreadable" when it could not be read.
type Sums map[string]string

const unreadable = "unreadable"

// read reads path and records its hash (when s is not nil).
func (s Sums) read(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if s != nil {
		switch {
		case err == nil:
			s[path] = hashBytes(data)
		case errors.Is(err, os.ErrNotExist):
			s[path] = ""
		default:
			s[path] = unreadable
		}
	}
	return data, err
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SumFile returns the hash of the file's current content, in the form Sums
// records it.
func SumFile(path string) string {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return hashBytes(data)
	case errors.Is(err, os.ErrNotExist):
		return ""
	default:
		return unreadable
	}
}

// SumCache memoizes SumFile results. A file is re-read only when its size or
// modification time changed since it was last summed, so a caller that checks
// files on a timer does not re-read and re-hash every one of them each tick.
// It is not safe for concurrent use.
type SumCache struct {
	entries map[string]sumEntry
}

type sumEntry struct {
	size    int64
	modTime int64
	sum     string
}

// Sum returns the content hash of path exactly as SumFile would, reusing the
// previous result while the file's size and modification time are unchanged.
func (c *SumCache) Sum(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		// No stat to trust (missing or unreadable): read it and forget any
		// cached value, so a file that appears later is noticed.
		delete(c.entries, path)
		return SumFile(path)
	}
	if e, ok := c.entries[path]; ok && e.size == fi.Size() && e.modTime == fi.ModTime().UnixNano() {
		return e.sum
	}
	sum := SumFile(path)
	if c.entries == nil {
		c.entries = make(map[string]sumEntry)
	}
	c.entries[path] = sumEntry{size: fi.Size(), modTime: fi.ModTime().UnixNano(), sum: sum}
	return sum
}

// Changed reports whether the current content of any path in want differs from
// the hash it records, like Sums.Changed, using the cache.
func (c *SumCache) Changed(want Sums) bool {
	for path, sum := range want {
		if c.Sum(path) != sum {
			return true
		}
	}
	return false
}

// Merge returns the union of the given sums.
func Merge(all ...Sums) Sums {
	out := Sums{}
	for _, s := range all {
		for k, v := range s {
			out[k] = v
		}
	}
	return out
}
