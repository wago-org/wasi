package p2

import (
	"context"
	"testing"
)

// Compare the adapter's wait directly with the typed-error boundary added to
// blocking operations. The already-ready success path needs no allocation.
func BenchmarkBlockingOutputWait(b *testing.B) {
	ctx := context.Background()
	out := NewOutputStream(nil)
	state := &hostState{permits: map[uint32]uint64{stdoutRep: maxIOSize}}
	b.Run("adapter", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := out.WaitWritable(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("stream-error-boundary", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if values, err := state.waitWritable(ctx, stdoutRep, out); values != nil || err != nil {
				b.Fatalf("unexpected wait result: %v, %v", values, err)
			}
		}
	})
	b.Run("post-flush-permit-probe", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := state.probeWritePermit(stdoutRep, out); err != nil {
				b.Fatal(err)
			}
		}
	})
}
