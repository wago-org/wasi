package p2

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
)

func timestampDescriptorForTest(t *testing.T, directory bool) *os.File {
	t.Helper()
	var file *os.File
	var err error
	if directory {
		file, err = openPreopenDirectory(t.TempDir(), true)
	} else {
		file, err = os.Create(filepath.Join(t.TempDir(), "file"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

// Establish the host's range and resolution using a separate native path.
// Go's os.Chtimes and Filetime.Nanoseconds also overflow after 2262.
func requireNativeTimestampControl(t *testing.T, file *os.File, access, modification time.Time) {
	t.Helper()
	if err := nativeTimestampControl(file, access, modification); err != nil {
		t.Skipf("native timestamp control unsupported: %v", err)
	}
	at, mt := nativeTimestampRead(t, file)
	if !at.Equal(access) || !mt.Equal(modification) {
		t.Skipf("host range/resolution: native access/modification = %v/%v, requested %v/%v", at, mt, access, modification)
	}
}

func TestDescriptorTimesPreservePrecisionAndRange(t *testing.T) {
	for _, tc := range []struct {
		name   string
		at, mt time.Time
	}{
		{"subsecond", time.Date(2007, 8, 9, 10, 11, 12, 123456700, time.UTC), time.Date(2008, 9, 10, 11, 12, 13, 987654300, time.UTC)},
		{"post2262", time.Date(2290, 8, 9, 10, 11, 12, 123456700, time.UTC), time.Date(2300, 9, 10, 11, 12, 13, 987654300, time.UTC)},
	} {
		for _, directory := range []bool{false, true} {
			kind := "file"
			if directory {
				kind = "directory"
			}
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				file := timestampDescriptorForTest(t, directory)
				requireNativeTimestampControl(t, file, tc.at, tc.mt)
				initial := time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC)
				if err := nativeTimestampControl(file, initial, initial); err != nil {
					t.Fatal(err)
				}
				if err := setDescriptorTimes(file, timestampForTest(tc.at), timestampForTest(tc.mt), time.Now); err != nil {
					t.Fatal(err)
				}
				at, mt := nativeTimestampRead(t, file)
				if !at.Equal(tc.at) || !mt.Equal(tc.mt) {
					t.Fatalf("descriptor access/modification = %v/%v, want %v/%v", at, mt, tc.at, tc.mt)
				}
			})
		}
	}
}

func TestDescriptorTimesNoChangePreservesFutureField(t *testing.T) {
	for _, changeAccess := range []bool{false, true} {
		name := "access unchanged"
		if changeAccess {
			name = "modification unchanged"
		}
		t.Run(name, func(t *testing.T) {
			file := timestampDescriptorForTest(t, false)
			future := time.Date(2300, 1, 2, 3, 4, 5, 123456700, time.UTC)
			requireNativeTimestampControl(t, file, future, future)
			if !changeAccess {
				// Some hosts update access time while querying descriptor type.
				// Establish that a stat can preserve it before testing no-change.
				if _, err := file.Stat(); err != nil {
					t.Fatal(err)
				}
				at, _ := nativeTimestampRead(t, file)
				if !at.Equal(future) {
					t.Skipf("host stat updates access time to %v", at)
				}
			}
			want := time.Date(2008, 9, 10, 11, 12, 13, 765432100, time.UTC)
			var access, modification component.Value = component.VariantValue{Disc: 0}, timestampForTest(want)
			wantAccess, wantModification := future, want
			if changeAccess {
				access, modification = modification, access
				wantAccess, wantModification = want, future
			}
			if err := setDescriptorTimes(file, access, modification, time.Now); err != nil {
				t.Fatal(err)
			}
			at, mt := nativeTimestampRead(t, file)
			if !at.Equal(wantAccess) || !mt.Equal(wantModification) {
				t.Fatalf("descriptor access/modification = %v/%v, want %v/%v", at, mt, wantAccess, wantModification)
			}
		})
	}
}

func BenchmarkSetFileTimes(b *testing.B) {
	file, err := os.Create(filepath.Join(b.TempDir(), "file"))
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	want := time.Date(2008, 9, 10, 11, 12, 13, 123456700, time.UTC)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := setFileTimes(file, want, want); err != nil {
			b.Fatal(err)
		}
	}
}
