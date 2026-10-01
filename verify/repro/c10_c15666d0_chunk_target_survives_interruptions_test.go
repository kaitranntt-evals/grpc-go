// How to run (branch evalon/grpc-go-tr-c15666d0): cp this file to internal/transport/verify_c10_chunk_target_test.go && go test -v -run '^TestVerifyC10_' google.golang.org/grpc/internal/transport -race -count=1
//
// C10: does the enlarged compaction chunk target (nextChunkSize) survive
// interruptions by larger frames, so that later chunks holding a single byte
// retain 16 KiB backing buffers from the default pool?
package transport

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/mem"
)

// vC10ExactPool hands out exactly-sized buffers and records the sizes
// requested, exposing the allocation target itself (no size-class rounding).
type vC10ExactPool struct{ requested []int }

func (p *vC10ExactPool) Get(n int) *[]byte {
	p.requested = append(p.requested, n)
	b := make([]byte, n)
	return &b
}
func (p *vC10ExactPool) Put(*[]byte) {}

type vC10Result struct {
	tinyCaps      []int // backing capacity of each queued entry holding tiny payload bytes
	tinyLens      []int
	nextTargets   []int // b.nextChunkSize after each tiny put
	tinyPayload   int
	tinyRetained  int
	largePayload  int
	largeRetained int
	entries       int
}

func vC10Run(t *testing.T, pool mem.BufferPool, tinySize, largeSize, rounds int) vC10Result {
	b := &recvBuffer{}
	b.init(pool)
	var want []byte
	var res vC10Result
	put := func(p []byte) {
		want = append(want, p...)
		b.put(recvMsg{buffer: mem.Copy(p, mem.DefaultBufferPool())})
	}
	put([]byte{0xEE}) // occupies the reader channel; everything below is backlog
	for i := 0; i < rounds; i++ {
		put(bytes.Repeat([]byte{byte(i + 1)}, tinySize))
		b.mu.Lock()
		res.nextTargets = append(res.nextTargets, b.nextChunkSize)
		b.mu.Unlock()
		put(bytes.Repeat([]byte{0x80 | byte(i)}, largeSize)) // larger frame interrupts tiny accumulation
	}
	b.mu.Lock()
	b.finalizeChunk()
	res.entries = len(b.backlog)
	for _, m := range b.backlog {
		d := m.buffer.ReadOnlyData()
		if len(d) <= recvBufferCompactionThreshold {
			res.tinyCaps = append(res.tinyCaps, cap(d))
			res.tinyLens = append(res.tinyLens, len(d))
			res.tinyPayload += len(d)
			res.tinyRetained += cap(d)
		} else {
			res.largePayload += len(d)
			res.largeRetained += cap(d)
		}
	}
	b.mu.Unlock()
	// All bytes must still be delivered in order.
	var got []byte
	for len(got) < len(want) {
		m := <-b.c
		b.load()
		got = append(got, m.buffer.ReadOnlyData()...)
		m.buffer.Free()
	}
	if !bytes.Equal(got, want) {
		t.Errorf("delivered bytes differ from the queued payloads")
	}
	return res
}

func vC10Ints(v []int) string { return strings.Trim(fmt.Sprint(v), "[]") }

func TestVerifyC10_OneByteChunksAfter2KiBInterruptions(t *testing.T) {
	const rounds = 16
	t.Logf("sequence: 1 byte (to channel), then %d x [1-byte frame, 2048-byte frame]; no reads", rounds)

	exact := &vC10ExactPool{}
	r := vC10Run(t, exact, 1, 2048, rounds)
	t.Logf("exact-size pool: nextChunkSize after each 1-byte put: %s", vC10Ints(r.nextTargets))
	t.Logf("exact-size pool: sizes requested from the pool for 1-byte chunks: %s", vC10Ints(exact.requested))
	t.Logf("exact-size pool: payload bytes per tiny chunk: %s", vC10Ints(r.tinyLens))
	t.Logf("exact-size pool: backing capacity per tiny chunk: %s", vC10Ints(r.tinyCaps))

	d := vC10Run(t, mem.DefaultBufferPool(), 1, 2048, rounds)
	t.Logf("default pool:    nextChunkSize after each 1-byte put: %s", vC10Ints(d.nextTargets))
	t.Logf("default pool:    payload bytes per tiny chunk: %s", vC10Ints(d.tinyLens))
	t.Logf("default pool:    backing capacity per tiny chunk: %s", vC10Ints(d.tinyCaps))
	n16k := 0
	for i, c := range d.tinyCaps {
		if c == 16384 && d.tinyLens[i] == 1 {
			n16k++
		}
	}
	t.Logf("default pool:    %d of %d one-byte chunks retain a 16384-byte backing buffer; tiny payload %d bytes held in %d bytes (%.0fx)",
		n16k, len(d.tinyCaps), d.tinyPayload, d.tinyRetained, float64(d.tinyRetained)/float64(d.tinyPayload))
	if n16k > 0 {
		t.Logf("C10 OBSERVED: the enlarged target survives the larger-frame interruptions; one-byte chunks retain 16 KiB buffers")
	} else {
		t.Logf("C10 NOT OBSERVED: no one-byte chunk retains a 16 KiB buffer")
	}
}

// Same traffic with compaction on vs. off: total backing capacity queued for a
// stream window's worth (64 KiB) of unread payload.
func TestVerifyC10_RetainedVsCompactionDisabled(t *testing.T) {
	for _, shape := range []struct {
		name          string
		tiny, large   int
		rounds        int
	}{
		{"1-byte + 2048-byte frames", 1, 2048, 31},
		{"5-byte gRPC message header frame + 1025-byte message frame", 5, 1025, 63},
	} {
		orig := envconfig.EnableReceiveBufferCompaction
		envconfig.EnableReceiveBufferCompaction = true
		on := vC10Run(t, mem.DefaultBufferPool(), shape.tiny, shape.large, shape.rounds)
		envconfig.EnableReceiveBufferCompaction = false
		off := vC10Run(t, mem.DefaultBufferPool(), shape.tiny, shape.large, shape.rounds)
		envconfig.EnableReceiveBufferCompaction = orig
		payload := on.tinyPayload + on.largePayload
		t.Logf("%s x %d (unread payload %d bytes):", shape.name, shape.rounds, payload)
		t.Logf("  compaction enabled : %d entries, tiny payload %d bytes in %d bytes of buffers; total retained %d bytes (%.1fx payload)",
			on.entries, on.tinyPayload, on.tinyRetained, on.tinyRetained+on.largeRetained, float64(on.tinyRetained+on.largeRetained)/float64(payload))
		t.Logf("  compaction disabled: %d entries, tiny payload %d bytes in %d bytes of buffers; total retained %d bytes (%.1fx payload)",
			off.entries, off.tinyPayload, off.tinyRetained, off.tinyRetained+off.largeRetained, float64(off.tinyRetained+off.largeRetained)/float64(payload))
	}
}
