package api

import (
	"container/list"
	"sync"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// gitDiffCache memoizes immutable commit diffs: a commit's diff never
// changes, so repeated views — and concurrent clients viewing the same
// commit — are served from memory instead of re-running git. Working-tree
// diffs are mutable and never enter the cache (the handler bypasses it).
// The zero value is an empty, ready cache.
type gitDiffCache struct {
	mu       sync.Mutex
	maxEntry int
	maxBytes int
	lru      *list.List                   // front = most recently used
	entries  map[gitDiffKey]*list.Element // key -> entry in lru
	bytes    int
	inflight map[gitDiffKey]*gitDiffFlight
}

// gitDiffKey identifies one cached diff. headHash is part of the key
// because it marks the commit's Head flag: when HEAD moves, old entries
// simply age out of the cache.
type gitDiffKey struct {
	dir, hash, pathFilter, headHash string
	ctxLines                        int
}

type gitDiffEntry struct {
	key   gitDiffKey
	res   *pb.GitDiffResult
	bytes int
}

// gitDiffFlight is one in-progress computation shared by concurrent callers.
type gitDiffFlight struct {
	done chan struct{}
	res  *pb.GitDiffResult
	err  error
}

// getOrCompute returns the cached diff for key, computing it when absent.
// Concurrent calls with the same key share one computation. Errors are not
// cached.
func (c *gitDiffCache) getOrCompute(key gitDiffKey, compute func() (*pb.GitDiffResult, error)) (*pb.GitDiffResult, error) {
	c.mu.Lock()
	c.initLocked()
	if el, ok := c.entries[key]; ok {
		c.lru.MoveToFront(el)
		res := el.Value.(*gitDiffEntry).res
		c.mu.Unlock()
		return res, nil
	}
	if f, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-f.done
		return f.res, f.err
	}
	f := &gitDiffFlight{done: make(chan struct{})}
	c.inflight[key] = f
	c.mu.Unlock()

	f.res, f.err = compute()

	c.mu.Lock()
	delete(c.inflight, key)
	if f.err == nil && f.res != nil {
		c.putLocked(key, f.res)
	}
	c.mu.Unlock()
	close(f.done)
	return f.res, f.err
}

func (c *gitDiffCache) initLocked() {
	if c.lru != nil {
		return
	}
	if c.maxEntry <= 0 {
		c.maxEntry = 32
	}
	if c.maxBytes <= 0 {
		c.maxBytes = 64 << 20
	}
	c.lru = list.New()
	c.entries = map[gitDiffKey]*list.Element{}
	c.inflight = map[gitDiffKey]*gitDiffFlight{}
}

func (c *gitDiffCache) putLocked(key gitDiffKey, res *pb.GitDiffResult) {
	if el, ok := c.entries[key]; ok {
		c.bytes -= el.Value.(*gitDiffEntry).bytes
		c.lru.Remove(el)
		delete(c.entries, key)
	}
	e := &gitDiffEntry{key: key, res: res, bytes: diffBytes(res)}
	c.entries[key] = c.lru.PushFront(e)
	c.bytes += e.bytes
	for c.bytes > c.maxBytes || c.lru.Len() > c.maxEntry {
		back := c.lru.Back()
		if back == nil {
			break
		}
		c.bytes -= back.Value.(*gitDiffEntry).bytes
		delete(c.entries, back.Value.(*gitDiffEntry).key)
		c.lru.Remove(back)
	}
}

// diffBytes approximates a diff's memory footprint.
func diffBytes(res *pb.GitDiffResult) int {
	return len(res.Diff) + len(res.Files)*256 + 1024
}
