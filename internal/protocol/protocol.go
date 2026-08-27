package protocol

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	localcomposev1 "github.com/blesswinsamuel/local-compose/internal/gen/proto/localcompose/v1"
)

// DefaultLogTail is the history window interactive frontends (web) and
// foreground `up` request. CLI `logs` defaults to 0 (all, subject to the
// server's byte cap).
const DefaultLogTail = 5000

// Type aliases to generated Protobuf types for convenience across the codebase.
type (
	DaemonInfo        = localcomposev1.DaemonInfo
	ServiceState      = localcomposev1.ServiceState
	ActionInfo        = localcomposev1.ActionInfo
	ActionState       = localcomposev1.ActionState
	ProjectInfo       = localcomposev1.ProjectInfo
	ServiceStat       = localcomposev1.ServiceStat
	PortBinding       = localcomposev1.PortBinding
	GitRef            = localcomposev1.GitRef
	GitBranch         = localcomposev1.GitBranch
	GitTag            = localcomposev1.GitTag
	GitStash          = localcomposev1.GitStash
	GitCommit         = localcomposev1.GitCommit
	GitFileChange     = localcomposev1.GitFileChange
	GitDiffResult     = localcomposev1.GitDiffResult
	LogChunk          = localcomposev1.LogChunk
	ActionOutputChunk = localcomposev1.ActionOutputChunk
	Event             = localcomposev1.Event
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
