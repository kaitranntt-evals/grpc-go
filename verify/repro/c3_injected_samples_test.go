// Run: sh verify/repro/c3_heap_delta.sh (applies c3_inject_heap_samples.patch on evalon/grpc-go-tr-8401755a, copies this file into internal/transport and runs `go test -tags verify_repro,verify_c3_inject -run 'TestVerifyC3Inject' -v ./internal/transport`).

//go:build verify_repro && verify_c3_inject

package transport

import (
	"testing"
)

// TestVerifyC3Inject replays the branch's own two memory-regression tests,
// unmodified, while scripting the values returned by heapAllocBytes() through
// the verify-only hook added by c3_inject_heap_samples.patch.
func TestVerifyC3Inject(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{"TestRecvBuffer_CompactsTinyPayloads", s{}.TestRecvBuffer_CompactsTinyPayloads},
		{"TestServerReceiveBufferCompaction_ManyTinyDataFrames", s{}.TestServerReceiveBufferCompaction_ManyTinyDataFrames},
	}
	cases := []struct {
		name string
		// sample returns the value for the call-th heapAllocBytes() call
		// (1 = "before", 2 = second sample), given the real sample and the
		// value that was returned for the first call.
		sample func(call int, real, first uint64) uint64
	}{
		{"control_no_injection", func(_ int, real, _ uint64) uint64 { return real }},
		{"second_sample_64KiB_lower_than_first", func(call int, real, first uint64) uint64 {
			if call == 2 {
				return first - 64*1024
			}
			return real
		}},
		{"second_sample_1_byte_lower_than_first", func(call int, real, first uint64) uint64 {
			if call == 2 {
				return first - 1
			}
			return real
		}},
		{"second_sample_invalid_zero", func(call int, real, _ uint64) uint64 {
			if call == 2 {
				return 0
			}
			return real
		}},
		{"first_sample_invalid_zero", func(call int, real, _ uint64) uint64 {
			if call == 1 {
				return 0
			}
			return real
		}},
		{"both_samples_invalid_zero", func(int, uint64, uint64) uint64 { return 0 }},
	}
	for _, tt := range tests {
		for _, c := range cases {
			var calls int
			var first uint64
			var returned []uint64
			verifyHeapHook = func(real uint64) uint64 {
				calls++
				v := c.sample(calls, real, first)
				if calls == 1 {
					first = v
				}
				returned = append(returned, v)
				return v
			}
			passed := t.Run(tt.name+"/"+c.name, tt.run)
			verifyHeapHook = nil
			verdict := "FAILED (measurement rejected)"
			if passed {
				verdict = "PASSED (measurement accepted)"
			}
			var delta uint64
			if len(returned) >= 2 {
				delta = returned[1] - returned[0]
			}
			t.Logf("OBSERVED %s / %s: heap samples=%v uint64(second-first)=%d -> branch test %s", tt.name, c.name, returned, delta, verdict)
		}
	}
}
