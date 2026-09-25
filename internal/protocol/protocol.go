package protocol

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	devyardv1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// DefaultLogTail is the history window interactive frontends (web) and
// foreground `up` request. CLI `logs` defaults to 0 (all, subject to the
// server's byte cap).
const DefaultLogTail = 5000

// Type aliases to generated Protobuf types for convenience across the codebase.
type (
	DaemonInfo      = devyardv1.DaemonInfo
	ServiceState    = devyardv1.ServiceState
	TaskState       = devyardv1.TaskState
	ProjectInfo     = devyardv1.ProjectInfo
	ServiceStat     = devyardv1.ServiceStat
	PortBinding     = devyardv1.PortBinding
	GitRef          = devyardv1.GitRef
	GitBranch       = devyardv1.GitBranch
	GitTag          = devyardv1.GitTag
	GitStash        = devyardv1.GitStash
	GitCommit       = devyardv1.GitCommit
	GitFileChange   = devyardv1.GitFileChange
	GitDiffResult   = devyardv1.GitDiffResult
	GitStatus       = devyardv1.GitStatus
	LogChunk        = devyardv1.LogChunk
	TaskOutputChunk = devyardv1.TaskOutputChunk
	Event           = devyardv1.Event
)

// TimeToProto converts a Go time.Time to a protobuf Timestamp. Returns nil if zero.
func TimeToProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// ProtoToTime converts a protobuf Timestamp to a Go time.Time. Returns time.Time{} if nil.
func ProtoToTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

// FormatTime converts a time.Time to the wire RFC3339Nano form, returning an
// empty string for the zero time.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
