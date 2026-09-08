package p2

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// TestWASI020Surface prevents hand-maintained registration drift. The golden
// file is generated from WebAssembly/wasi tag v0.2.0, commit
// 70214b878af4ce45889b4ad9d26a7ac98db8931b, by cmd/wit-surface.
func TestWASI020Surface(t *testing.T) {
	data, err := os.ReadFile("testdata/wasi-0.2.0.surface")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(data))
	var got []string
	surfaceRecorder.Lock()
	surfaceRecorder.fn = func(iface, name string) { got = append(got, iface+"#"+name) }
	surfaceRecorder.Unlock()
	Options(Config{})
	surfaceRecorder.Lock()
	surfaceRecorder.fn = nil
	surfaceRecorder.Unlock()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("WASI 0.2 surface mismatch\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
