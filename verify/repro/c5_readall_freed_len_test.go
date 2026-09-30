// Run (after verify/repro/setup_worktrees.sh): cp verify/repro/c5_readall_freed_len_test.go /tmp/claims/97f9441e/internal/transport/ && (cd /tmp/claims/97f9441e && go test ./internal/transport -run '^TestVerifyC5' -count=1 -v); rm /tmp/claims/97f9441e/internal/transport/c5_readall_freed_len_test.go -- add VERIFY_C5_UNCAUGHT=1 and -run '^TestVerifyC5_Uncaught|^TestVerifyC5_After' to see the panic abort the test binary
package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"

	"google.golang.org/grpc/mem"
)

// callReadAll invokes the branch's own readAll helper (recv_buffer_test.go)
// and converts a panic into a returned value so every case can be reported.
func callReadAll(s *Stream, n, size int) (got []byte, err error, panicked any) {
	defer func() { panicked = recover() }()
	got, err = readAll(s, n, size)
	return got, err, nil
}

func TestVerifyC5_ReadAllUnexpectedData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	tests := []struct {
		name      string
		write     func(st *Stream)
		n, size   int
		wantPanic bool
	}{
		{
			// One surplus byte arrives as its own pooled buffer (capacity 4096,
			// built with the branch's own pooledBuffer helper).
			name: "surplus_byte_in_own_pooled_buffer",
			write: func(st *Stream) {
				st.write(recvMsg{buffer: pooledBuffer([]byte{0xAB}, 4096)})
			},
			n: 0, size: 100, wantPanic: true,
		},
		{
			// A 2000-byte pooled frame, of which the caller expects 1999 bytes:
			// the surplus byte is the tail split of the pooled buffer.
			name: "surplus_byte_is_tail_of_pooled_frame",
			write: func(st *Stream) {
				st.write(recvMsg{buffer: pooledBuffer(make([]byte, 2000), 4096)})
			},
			n: 1999, size: 1000, wantPanic: true,
		},
		{
			// Control: surplus byte backed by a heap SliceBuffer; the intended
			// diagnostic is produced.
			name: "control_surplus_byte_in_slice_buffer",
			write: func(st *Stream) {
				st.write(recvMsg{buffer: mem.SliceBuffer{0xAB}})
			},
			n: 0, size: 100, wantPanic: false,
		},
		{
			// Control: no surplus data; readAll returns the terminal error.
			name: "control_no_surplus",
			write: func(st *Stream) {
				st.write(recvMsg{buffer: pooledBuffer(make([]byte, 2000), 4096)})
				st.write(recvMsg{err: io.EOF})
			},
			n: 2000, size: 1000, wantPanic: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newRecvBufferTestStream(ctx)
			tc.write(st)
			got, err, p := callReadAll(st, tc.n, tc.size)
			t.Logf("readAll returned len(got)=%d err=%v panic=%v", len(got), err, p)
			if (p != nil) != tc.wantPanic {
				t.Errorf("panic=%v, wantPanic=%v", p, tc.wantPanic)
			}
			if p != nil && fmt.Sprint(p) != "Cannot read freed buffer" {
				t.Errorf("unexpected panic value %v", p)
			}
		})
	}
}

// TestVerifyC5_Uncaught calls readAll the way the branch's tests do (no
// recover). With surplus pooled data the test binary aborts instead of
// reporting "read 1 unexpected bytes after N bytes".
func TestVerifyC5_Uncaught(t *testing.T) {
	if os.Getenv("VERIFY_C5_UNCAUGHT") == "" {
		t.Skip("set VERIFY_C5_UNCAUGHT=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	st := newRecvBufferTestStream(ctx)
	st.write(recvMsg{buffer: pooledBuffer(make([]byte, 2000), 4096)})
	if _, err := readAll(st, 1999, 1000); err != io.EOF {
		t.Fatalf("readAll() = %v, want %v", err, io.EOF)
	}
}

// TestVerifyC5_After sorts after TestVerifyC5_Uncaught; it never runs when
// the panic aborts the binary.
func TestVerifyC5_After(t *testing.T) { t.Log("ran") }
