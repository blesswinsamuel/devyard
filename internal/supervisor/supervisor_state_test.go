package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/supervisor"
)

func TestStateSaveAndLoad(t *testing.T) {
	t.Parallel()
	locs := testLocations(t)

	file := fileWith(map[string]config.Service{
		"s1": {Command: "sleep 10"},
	})
	s, err := supervisor.New(supervisor.Options{
		Locations:  locs,
		File:       file,
		Order:      []string{"s1"},
		BaseDir:    t.TempDir(),
		Foreground: false,
		Backoff:    testBackoff(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop(context.Background())

	// Give s1 time to start
	time.Sleep(50 * time.Millisecond)

	if err := s.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	snap, err := supervisor.LoadState(locs)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if snap == nil {
		t.Fatalf("expected state snapshot, got nil")
	}

	s1, ok := snap.Services["s1"]
	if !ok {
		t.Fatalf("s1 missing from state snapshot")
	}
	if s1.PID <= 0 {
		t.Errorf("expected PID > 0, got %d", s1.PID)
	}
	if s1.PGID <= 0 {
		t.Errorf("expected PGID > 0, got %d", s1.PGID)
	}
	if !supervisor.IsProcessGroupAlive(s1.PGID) {
		t.Errorf("expected process group %d to be alive", s1.PGID)
	}
}
