package api

import (
	"context"
	"time"

	"connectrpc.com/connect"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/procstat"
)

// Stats streams CPU and memory usage of a project's running processes at
// a fixed interval. The first message arrives after one interval (CPU%
// needs two samples).
func (s *Server) Stats(ctx context.Context, req *connect.Request[pb.StatsRequest], stream *connect.ServerStream[pb.StatsResponse]) error {
	if _, err := s.project(req.Msg.Project); err != nil {
		return err
	}
	interval := time.Duration(req.Msg.IntervalMs) * time.Millisecond
	if interval < 200*time.Millisecond {
		interval = time.Second
	}
	type key struct {
		kind, name string
		pid        int32
	}
	type prevSample struct {
		cpu time.Duration
		at  time.Time
	}
	prev := map[key]prevSample{}
	sample := func() []*pb.ProcessStat {
		now := time.Now()
		var out []*pb.ProcessStat
		next := map[key]prevSample{}
		visit := func(kind, name string, pid int32) {
			if pid <= 0 {
				return
			}
			k := key{kind, name, pid}
			sm, ok := procstat.SampleGroup(int(pid))
			if !ok {
				return
			}
			next[k] = prevSample{cpu: sm.CPU, at: now}
			stat := &pb.ProcessStat{Kind: kind, Name: name, Pid: pid, Procs: int32(sm.Procs), RssBytes: sm.RSS}
			if p, ok := prev[k]; ok {
				if dt := now.Sub(p.at); dt > 0 {
					stat.CpuPercent = float64(sm.CPU-p.cpu) / float64(dt) * 100
					if stat.CpuPercent < 0 {
						stat.CpuPercent = 0
					}
				}
			}
			out = append(out, stat)
		}
		for _, svc := range s.Bus.Services(req.Msg.Project) {
			visit("service", svc.Name, svc.Pid)
		}
		for _, t := range s.Bus.Tasks(req.Msg.Project) {
			visit("task", t.Name, t.Pid)
		}
		prev = next
		return out
	}
	sample()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		if err := stream.Send(&pb.StatsResponse{TsUnixMs: time.Now().UnixMilli(), Stats: sample()}); err != nil {
			return err
		}
	}
}
