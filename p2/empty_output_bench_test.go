package p2

import (
	"context"
	"io"
	"testing"
)

func BenchmarkEmptyOutputAdapterWrite(b *testing.B) {
	out := NewOutputStream(io.Discard)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := out.TryWrite(nil); err != nil {
			b.Fatal(err)
		}
		if err := out.WaitWritable(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
