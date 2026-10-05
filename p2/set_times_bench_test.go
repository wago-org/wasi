package p2

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkSetTimesUnderPathFlags(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("data"), 0o600); err != nil {
		b.Fatal(err)
	}
	base, err := openPreopenDirectory(root, true)
	if err != nil {
		b.Fatal(err)
	}
	defer base.Close()
	want := timestampForTest(time.Date(2006, time.July, 8, 9, 10, 11, 0, time.UTC))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := setTimesUnderPathFlags(base, "file", 0, want, want, time.Now); err != nil {
			b.Fatal(err)
		}
	}
}
