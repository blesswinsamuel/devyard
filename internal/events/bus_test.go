package events

import (
	"context"
	"testing"
	"time"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

func TestSnapshotThenChanges(t *testing.T) {
	b := New()
	b.UpsertProject(&pb.Project{Id: "p", Status: "stopped"})
	sub, snap := b.Subscribe()
	defer sub.Close()
	if len(snap.GetSnapshot().Projects) != 1 || snap.Revision != 1 {
		t.Fatalf("snapshot: %+v", snap)
	}
	b.UpsertService(&pb.Service{Project: "p", Name: "a", Status: "running"})
	b.UpsertService(&pb.Service{Project: "p", Name: "a", Status: "running"}) // duplicate: dropped
	b.RemoveService("p", "a")
	ctx := context.Background()
	ev1, _ := sub.Next(ctx)
	ev2, _ := sub.Next(ctx)
	if ev1.Revision != 2 || ev1.GetChange().GetService().GetName() != "a" {
		t.Fatalf("ev1: %+v", ev1)
	}
	if ev2.Revision != 3 || ev2.GetChange().GetRemoved().GetKind() != KindService {
		t.Fatalf("ev2: %+v", ev2)
	}
}

func TestOverflowYieldsFreshSnapshot(t *testing.T) {
	b := New()
	sub, _ := b.Subscribe()
	defer sub.Close()
	for i := range subscriberBuffer + 10 {
		b.UpsertService(&pb.Service{Project: "p", Name: "a", Restarts: int32(i)})
	}
	ev, err := sub.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snap := ev.GetSnapshot()
	if snap == nil {
		t.Fatalf("expected resync snapshot, got %+v", ev)
	}
	if got := snap.Services[0].Restarts; got != int32(subscriberBuffer+9) {
		t.Fatalf("snapshot not current: restarts=%d", got)
	}
	// After the resync, changes flow again.
	b.UpsertService(&pb.Service{Project: "p", Name: "a", Restarts: -1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ev, err = sub.Next(ctx)
	if err != nil || ev.GetChange().GetService().GetRestarts() != -1 {
		t.Fatalf("after resync: %+v %v", ev, err)
	}
}

func TestRemoveProjectCascades(t *testing.T) {
	b := New()
	b.UpsertProject(&pb.Project{Id: "p"})
	b.UpsertService(&pb.Service{Project: "p", Name: "a"})
	b.UpsertTask(&pb.Task{Project: "p", Name: "t"})
	b.UpsertGit(&pb.GitStatus{Project: "p"})
	b.UpsertService(&pb.Service{Project: "q", Name: "a"})
	b.RemoveProject("p")
	_, snap := b.Snapshot()
	if len(snap.Projects) != 0 || len(snap.Tasks) != 0 || len(snap.Git) != 0 || len(snap.Services) != 1 {
		t.Fatalf("snapshot after remove: %+v", snap)
	}
}
