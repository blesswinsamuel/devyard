package api

import (
	"errors"
	"sync"
	"testing"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

func diffFor() *pb.GitDiffResult {
	return &pb.GitDiffResult{Commit: &pb.GitCommit{Hash: "h"}, Diff: "diff"}
}

func TestGitDiffCacheHit(t *testing.T) {
	c := &gitDiffCache{}
	key := gitDiffKey{dir: "/d", hash: "abc", ctxLines: 3}
	calls := 0
	compute := func() (*pb.GitDiffResult, error) {
		calls++
		return diffFor(), nil
	}
	first, err := c.getOrCompute(key, compute)
	if err != nil {
		t.Fatalf("getOrCompute: %v", err)
	}
	second, err := c.getOrCompute(key, compute)
	if err != nil {
		t.Fatalf("getOrCompute: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 computation, got %d", calls)
	}
	if first != second {
		t.Fatalf("expected the cached instance, got different results")
	}
}

func TestGitDiffCacheErrorNotCached(t *testing.T) {
	c := &gitDiffCache{}
	key := gitDiffKey{dir: "/d", hash: "abc"}
	calls := 0
	if _, err := c.getOrCompute(key, func() (*pb.GitDiffResult, error) {
		calls++
		return nil, errors.New("boom")
	}); err == nil {
		t.Fatal("expected the compute error to surface")
	}
	if _, err := c.getOrCompute(key, func() (*pb.GitDiffResult, error) {
		calls++
		return diffFor(), nil
	}); err != nil {
		t.Fatalf("expected the retry to succeed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected the error to not be cached (2 computations), got %d", calls)
	}
}

func TestGitDiffCacheSingleFlight(t *testing.T) {
	c := &gitDiffCache{}
	key := gitDiffKey{dir: "/d", hash: "abc"}
	release := make(chan struct{})
	calls := 0
	var mu sync.Mutex
	compute := func() (*pb.GitDiffResult, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return diffFor(), nil
	}

	const n = 8
	results := make([]*pb.GitDiffResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			results[i], errs[i] = c.getOrCompute(key, compute)
		}()
	}
	close(release)
	wg.Wait()

	if calls != 1 {
		t.Fatalf("expected concurrent calls to share one computation, got %d", calls)
	}
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if results[i] != results[0] {
			t.Fatalf("expected all waiters to get the computed instance, call %d differs", i)
		}
	}
}

func TestGitDiffCacheEvictionEntryLimit(t *testing.T) {
	c := &gitDiffCache{maxEntry: 2}
	keys := []gitDiffKey{
		{dir: "/d", hash: "a"},
		{dir: "/d", hash: "b"},
		{dir: "/d", hash: "c"},
	}
	for _, k := range keys {
		if _, err := c.getOrCompute(k, func() (*pb.GitDiffResult, error) { return diffFor(), nil }); err != nil {
			t.Fatalf("getOrCompute: %v", err)
		}
	}
	// "a" is the least recently used and must have been evicted.
	if _, ok := c.entries[keys[0]]; ok {
		t.Fatalf("expected the oldest entry to be evicted, cache holds %v", c.entries)
	}
	// Touch "b" so it survives the next insert.
	if _, err := c.getOrCompute(keys[1], func() (*pb.GitDiffResult, error) {
		t.Fatal("expected a cache hit")
		return nil, nil
	}); err != nil {
		t.Fatalf("getOrCompute: %v", err)
	}
	// Inserting "d" evicts "c", not the recently used "b".
	if _, err := c.getOrCompute(gitDiffKey{dir: "/d", hash: "d"}, func() (*pb.GitDiffResult, error) {
		return diffFor(), nil
	}); err != nil {
		t.Fatalf("getOrCompute: %v", err)
	}
	if _, ok := c.entries[keys[1]]; !ok {
		t.Fatal("expected the touched entry to survive eviction")
	}
	if _, ok := c.entries[keys[2]]; ok {
		t.Fatal("expected the untouched entry to be evicted")
	}
}

func TestGitDiffCacheEvictionByteLimit(t *testing.T) {
	c := &gitDiffCache{maxEntry: 32, maxBytes: 4096}
	key := func(i int) gitDiffKey { return gitDiffKey{dir: "/d", hash: string(rune('a' + i))} }
	for i := range 4 {
		res := &pb.GitDiffResult{Commit: &pb.GitCommit{Hash: "h"}, Diff: string(make([]byte, 1500))}
		if _, err := c.getOrCompute(key(i), func() (*pb.GitDiffResult, error) { return res, nil }); err != nil {
			t.Fatalf("getOrCompute: %v", err)
		}
	}
	if c.lru.Len() != 1 {
		t.Fatalf("expected the byte cap to evict down to one entry, got %d", c.lru.Len())
	}
	if c.bytes > c.maxBytes {
		t.Fatalf("cache over budget: %d > %d", c.bytes, c.maxBytes)
	}
}
