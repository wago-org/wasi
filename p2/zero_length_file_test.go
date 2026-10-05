package p2

import (
	component "github.com/wago-org/component-model"
	"os"
	"testing"
)

func TestZeroLengthFileStreamClosure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "input-")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := newFilesystem(nil, Limits{})
	rep, err := s.addStream(&fileStream{file: f, read: true})
	if err != nil {
		t.Fatal(err)
	}
	expect := func(n uint64, closed bool) {
		t.Helper()
		values, err := s.readStream(rep, n)
		if err != nil {
			t.Fatal(err)
		}
		result := values[0].(component.ResultValue)
		if result.IsErr != closed {
			t.Fatalf("readStream(%d)=%v, wantClosed=%v", n, result, closed)
		}
		if closed && result.Payload.(component.VariantValue).Disc != 1 {
			t.Fatalf("closed result=%v", result)
		}
	}
	expect(0, false) // No EOF has been observed yet.
	expect(1, true)
	expect(0, true)
	expect(1, true)
}
