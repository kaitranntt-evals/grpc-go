package server

import (
 "reflect"
 "testing"
 "time"
)

// This hook only pauses the fixture between its counter publication and event record.
var reviewAfterFilterCounter func()

func TestReviewFixtureCompletionCounter(t *testing.T) {
 f := &evalRDSFilterBuilder{}
 (&evalRDSInterceptor{f: f}).Close()
 (&evalRDSInterceptor{f: f}).Close()
 published := make(chan struct{})
 release := make(chan struct{})
 done := make(chan struct{})
 reviewAfterFilterCounter = func() { close(published); <-release }
 go func() { defer close(done); f.Close() }()
 defer func() {
  close(release)
  select {
  case <-done:
  case <-time.After(5*time.Second): t.Error("filter cleanup did not complete"); return
  }
  reviewAfterFilterCounter = nil
  f.mu.Lock()
  final := append([]string(nil), f.orderEvents...)
  f.mu.Unlock()
  t.Logf("completed cleanup events: %v", final)
  if !reflect.DeepEqual(final, []string{"icpt-close", "icpt-close", "filter-close"}) {
   t.Errorf("cleanup actually violated dependency order: %v", final)
  }
 }()
 select {
 case <-published:
 case <-time.After(5*time.Second): t.Fatal("counter publication not reached")
 }
 // Both completion predicates used by the fixture have now succeeded.
 t.Logf("fixture completion counters: interceptors=%d filters=%d", f.closed.Load(), f.filterDestroyed.Load())
 if f.closed.Load() != 2 || f.filterDestroyed.Load() != 1 { t.Fatal("completion predicates not met") }
 f.mu.Lock()
 snap := append([]string(nil), f.orderEvents...)
 f.mu.Unlock()
 // Same final order assertion as the two eval tests.
 firstFilterCloseIdx := -1
 lastIcptCloseIdx := -1
 for idx, ev := range snap {
  if ev == "filter-close" && firstFilterCloseIdx == -1 { firstFilterCloseIdx = idx }
  if ev == "icpt-close" { lastIcptCloseIdx = idx }
 }
 if firstFilterCloseIdx < lastIcptCloseIdx {
  t.Errorf("fixture falsely reports dependency violation: filter index=%d interceptor index=%d events=%v", firstFilterCloseIdx, lastIcptCloseIdx, snap)
 }
}
