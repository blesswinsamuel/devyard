package api

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/logstore"
	"github.com/blesswinsamuel/devyard/internal/ui"
)

const (
	// maxTail bounds history sent in one request.
	maxTail = 20000
	// maxBatch bounds lines per response message.
	maxBatch = 1000
)

type logSource struct {
	src *pb.LogSource
	dir string
}

func (s *Server) Logs(ctx context.Context, req *connect.Request[pb.LogsRequest], stream *connect.ServerStream[pb.LogsResponse]) error {
	p, err := s.project(req.Msg.Project)
	if err != nil {
		return err
	}
	v := p.View()
	var sources []logSource
	reqSources := req.Msg.Sources
	if len(reqSources) == 0 {
		for _, name := range v.Order {
			reqSources = append(reqSources, &pb.LogSource{Kind: "service", Name: name})
		}
	}
	for _, src := range reqSources {
		switch src.Kind {
		case "service":
			def, ok := v.ServiceDefs[src.Name]
			if !ok {
				return connect.NewError(connect.CodeNotFound, errors.New("unknown service "+src.Name))
			}
			sources = append(sources, logSource{src: src, dir: def.ProcDir})
		case "task":
			def, ok := v.TaskDefs[src.Name]
			if !ok {
				return connect.NewError(connect.CodeNotFound, errors.New("unknown task "+src.Name))
			}
			sources = append(sources, logSource{src: src, dir: def.ProcDir})
		default:
			return invalid("source kind must be service or task")
		}
	}
	tail := int(req.Msg.Tail)
	if tail <= 0 || tail > maxTail {
		tail = maxTail
	}
	offset := req.Msg.RunOffset
	if offset > 0 {
		return invalid("run_offset must be 0 or negative")
	}
	resolveRun := func(dir string) (int64, error) {
		if req.Msg.Run > 0 {
			return req.Msg.Run, nil
		}
		return logstore.ResolveRun(dir, offset)
	}
	follow := req.Msg.Follow && offset == 0
	if req.Msg.Run > 0 && len(sources) == 1 {
		follow = follow && req.Msg.Run == logstore.LatestRun(sources[0].dir)
	}

	// Paging backwards through one source.
	if req.Msg.BeforeSeq > 0 {
		if len(sources) != 1 {
			return invalid("before_seq requires exactly one source")
		}
		src := sources[0]
		run, err := resolveRun(src.dir)
		if err != nil {
			return toConnect(err)
		}
		lines, more, err := logstore.Tail(src.dir, run, tail, req.Msg.BeforeSeq)
		if err != nil {
			return toConnect(err)
		}
		return sendLines(stream, toProto(src.src, lines), more, nil)
	}

	// History.
	var history []*pb.LogLine
	more := false
	runs := make([]int64, len(sources))
	lastSeq := make([]uint64, len(sources))
	for i, src := range sources {
		run, err := resolveRun(src.dir)
		if err != nil {
			if errors.Is(err, logstore.ErrNoRun) && (offset == 0 || len(sources) > 1) {
				continue
			}
			return toConnect(err)
		}
		runs[i] = run
		lines, m, err := logstore.Tail(src.dir, run, tail, 0)
		if err != nil {
			if errors.Is(err, logstore.ErrNoRun) {
				continue
			}
			return toConnect(err)
		}
		more = more || m
		if len(lines) > 0 {
			lastSeq[i] = lines[len(lines)-1].Seq
		}
		history = append(history, toProto(src.src, lines)...)
	}
	if len(sources) > 1 {
		sortLines(history)
		if len(history) > tail {
			history = history[len(history)-tail:]
			more = true
		}
	}
	if err := sendLines(stream, history, more, nil); err != nil {
		return err
	}
	if !follow {
		return nil
	}

	followers := make([]*logstore.Follower, len(sources))
	for i, src := range sources {
		followers[i] = logstore.NewFollower(src.dir, runs[i], lastSeq[i])
		defer followers[i].Close()
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		var batch []*pb.LogLine
		var newRun []*pb.LogSource
		for i, fl := range followers {
			lines, switched, err := fl.Poll()
			if err != nil {
				return toConnect(err)
			}
			if switched {
				newRun = append(newRun, sources[i].src)
			}
			batch = append(batch, toProto(sources[i].src, lines)...)
		}
		if len(batch) == 0 && len(newRun) == 0 {
			continue
		}
		if len(sources) > 1 {
			sortLines(batch)
		}
		if err := sendLines(stream, batch, false, newRun); err != nil {
			return err
		}
	}
}

func sortLines(lines []*pb.LogLine) {
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].TsUnixNanos < lines[j].TsUnixNanos })
}

func sendLines(stream *connect.ServerStream[pb.LogsResponse], lines []*pb.LogLine, more bool, newRun []*pb.LogSource) error {
	first := true
	for first || len(lines) > 0 {
		n := min(len(lines), maxBatch)
		resp := &pb.LogsResponse{Lines: lines[:n]}
		if first {
			resp.HasMoreBefore = more
			resp.NewRun = newRun
			first = false
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
		lines = lines[n:]
	}
	return nil
}

func toProto(src *pb.LogSource, lines []logstore.Line) []*pb.LogLine {
	out := make([]*pb.LogLine, len(lines))
	for i, l := range lines {
		out[i] = &pb.LogLine{
			Source:      src,
			Run:         l.Run,
			Seq:         l.Seq,
			TsUnixNanos: l.TS,
			Stream:      l.Stream.String(),
			Text:        cleanText(l.Text),
		}
	}
	return out
}

// cleanText keeps what a terminal would show for a line: the text after the
// last carriage return (progress bars redraw with \r), with only SGR
// sequences preserved.
func cleanText(s string) string {
	if i := strings.LastIndexByte(strings.TrimRight(s, "\r"), '\r'); i >= 0 {
		s = s[i+1:]
	}
	return ui.CleanLogLine(s)
}
