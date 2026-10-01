package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sourcesSum fingerprints the files matched by absolute glob patterns (`**`
// matches any number of directories) by path, size and modification time.
func sourcesSum(patterns []string) (string, error) {
	files := map[string]fs.FileInfo{}
	for _, pattern := range patterns {
		root := globRoot(pattern)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !matchGlob(pattern, path) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			files[path] = info
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		info := files[p]
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d\x00", p, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// globRoot is the longest directory prefix of pattern without wildcards.
func globRoot(pattern string) string {
	parts := strings.Split(pattern, string(filepath.Separator))
	var root []string
	for _, p := range parts[:len(parts)-1] {
		if strings.ContainsAny(p, `*?[\`) {
			break
		}
		root = append(root, p)
	}
	r := strings.Join(root, string(filepath.Separator))
	if r == "" {
		return string(filepath.Separator)
	}
	return r
}

// matchGlob matches path against pattern segment by segment; a `**`
// segment matches zero or more segments.
func matchGlob(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := filepath.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

func buildSumPath(procDir string) string { return filepath.Join(procDir, "build.sum") }

func readBuildSum(procDir string) string {
	data, err := os.ReadFile(buildSumPath(procDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeBuildSum records the fingerprint of the last successful build. A
// failed write only means the next start builds again.
func writeBuildSum(procDir, sum string) {
	_ = os.WriteFile(buildSumPath(procDir), []byte(sum+"\n"), 0o644)
}
