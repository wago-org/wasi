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

func TestFileStreamReadsGrowthBeforeReportedClosure(t *testing.T) {
	for _, length := range []uint64{3, 8} {
		name := "exact-read"
		if length > 3 {
			name = "partial-read-with-EOF"
		}
		t.Run(name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "growing-input-")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			writeAt := func(contents string, offset int64) {
				t.Helper()
				if _, err := f.WriteAt([]byte(contents), offset); err != nil {
					t.Fatal(err)
				}
			}
			writeAt("abc", 0)
			s := newFilesystem(nil, Limits{})
			rep, err := s.addStream(&fileStream{file: f, read: true})
			if err != nil {
				t.Fatal(err)
			}
			read := func(length uint64, contents string, closed bool) {
				t.Helper()
				values, err := s.readStream(rep, length)
				if err != nil {
					t.Fatal(err)
				}
				result := values[0].(component.ResultValue)
				if result.IsErr != closed {
					t.Fatalf("readStream(%d)=%v, want contents=%q closed=%v", length, result, contents, closed)
				}
				if closed {
					if result.Payload.(component.VariantValue).Disc != 1 {
						t.Fatalf("closed result=%v", result)
					}
				} else if got := string(result.Payload.([]byte)); got != contents {
					t.Fatalf("readStream(%d)=%q, want %q", length, got, contents)
				}
			}
			read(length, "abc", false)
			writeAt("def", 3)
			read(length, "def", false) // A partial ReadAt's EOF must not hide growth.
			read(0, "", false)         // Returning bytes has not reported closure.
			read(1, "", true)
			writeAt("ghi", 6)
			read(0, "", true) // Closure already reported remains terminal.
			read(8, "", true)
		})
	}
}
