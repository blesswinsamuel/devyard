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

// Changed lists the paths whose current content no longer matches s.
func (s Sums) Changed() []string {
	var out []string
	for path, sum := range s {
		if SumFile(path) != sum {
			out = append(out, path)
		}
	}
	return out
}
