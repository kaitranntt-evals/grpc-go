// Run (on branch evalon/grpc-go-xd-c46d6b0a): cp verify/repro/c2_release_negative_refcount_test.go internal/credentials/xds/ && go test -tags verify_c2 ./internal/credentials/xds -run '^Test/VerifyC2_' -count=1 -v
//
//go:build verify_c2

// Repro for C2: drive HandshakeInfo.Release past zero (the reference-count
// decrement path on this branch) while capturing everything grpclog emits, and
// compare with grpcsync.RefCounted.Decrement, which logs "Refcount cannot be
// negative" for the same misuse.
package xds

import (
	"bytes"
	"strings"
	"testing"

	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/grpcsync"
)

// captureGRPCLog routes all grpclog output (info/warning/error) into a buffer
// for the duration of the test.
func captureGRPCLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(buf, buf, buf, 99))
	return buf
}

// VerifyC2_ReleaseBelowZeroEmitsNoDiagnostic: Release() on a HandshakeInfo
// whose count is already zero. The branch detects refs < 0 (and resets the
// counter to 0) but emits nothing to grpclog.
func (s) TestVerifyC2_ReleaseBelowZeroEmitsNoDiagnostic(t *testing.T) {
	buf := captureGRPCLog(t)

	root, identity := &closeRecordingProvider{}, &closeRecordingProvider{}
	hi := NewHandshakeInfo(root, identity, nil, false, "", false, false)
	if got := hi.refs.Load(); got != 1 {
		t.Fatalf("initial refs = %d, want 1", got)
	}

	hi.Release() // 1 -> 0: legitimately closes the providers.
	if got := hi.refs.Load(); got != 0 || root.closeCount != 1 || identity.closeCount != 1 {
		t.Fatalf("after first Release: refs=%d root.closeCount=%d identity.closeCount=%d, want 0/1/1", got, root.closeCount, identity.closeCount)
	}
	logsBefore := buf.String()

	hi.Release() // 0 -> -1: ownership misuse; the branch's `refs < 0` path runs.
	t.Logf("refs after extra Release = %d (reset to 0 by the refs<0 branch)", hi.refs.Load())
	t.Logf("providers closed %d/%d times (no double close)", root.closeCount, identity.closeCount)

	extra := strings.TrimPrefix(buf.String(), logsBefore)
	t.Logf("grpclog output emitted by the extra Release(): %q", extra)
	if strings.Contains(extra, "negative") || strings.Contains(extra, "Refcount") || strings.Contains(extra, "ref") || strings.Contains(extra, "Release") {
		t.Fatalf("a diagnostic WAS emitted for the negative count: %q", extra)
	}
	if extra != "" {
		t.Fatalf("unexpected log output (not a refcount diagnostic, but not silent either): %q", extra)
	}
	t.Errorf("FINDING: HandshakeInfo.Release() took the count below zero (detected: counter reset to 0, no double close) but emitted NO diagnostic; grpclog captured %d bytes", len(extra))
}

// VerifyC2_GrpcsyncDecrementLogsForComparison: the codebase's generic
// reference counter does emit a diagnostic on the same misuse.
func (s) TestVerifyC2_GrpcsyncDecrementLogsForComparison(t *testing.T) {
	buf := captureGRPCLog(t)
	rc, err := grpcsync.NewRefCounted(struct{}{}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	rc.Decrement() // 1 -> 0
	rc.Decrement() // 0 -> -1
	got := buf.String()
	t.Logf("grpclog output from grpcsync.RefCounted.Decrement below zero: %q", got)
	if !strings.Contains(got, "Refcount cannot be negative") {
		t.Fatalf("expected grpcsync diagnostic, got %q", got)
	}
}
