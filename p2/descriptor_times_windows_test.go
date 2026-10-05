//go:build windows

package p2

import (
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestDescriptorTimesRejectsUnrepresentableWindowsTime(t *testing.T) {
	for _, accessInvalid := range []bool{false, true} {
		name := "modification overflow"
		if accessInvalid {
			name = "access overflow"
		}
		t.Run(name, func(t *testing.T) {
			file := timestampDescriptorForTest(t, false)
			initial := time.Date(2000, 1, 2, 3, 4, 5, 123456700, time.UTC)
			requireNativeTimestampControl(t, file, initial, initial)
			at, mt := time.Unix(1700000000, 123456700), time.Unix(math.MaxInt64, 0)
			if accessInvalid {
				at, mt = mt, at
			}
			if err := setFileTimes(file, at, mt); !errors.Is(err, hostFS.EOVERFLOW) {
				t.Fatalf("unrepresentable timestamp error = %v, want overflow", err)
			}
			gotAt, gotMt := nativeTimestampRead(t, file)
			if !gotAt.Equal(initial) || !gotMt.Equal(initial) {
				t.Fatalf("failed timestamp request mutated access/modification = %v/%v", gotAt, gotMt)
			}
		})
	}
}

func nativeTimestampControl(file *os.File, access, modification time.Time) error {
	filetime := func(value time.Time) windows.Filetime {
		ticks := uint64(value.Unix()+11644473600)*10000000 + uint64(value.Nanosecond()/100)
		return windows.Filetime{LowDateTime: uint32(ticks), HighDateTime: uint32(ticks >> 32)}
	}
	at, mt := filetime(access), filetime(modification)
	return windows.SetFileTime(windows.Handle(file.Fd()), nil, &at, &mt)
}

func nativeTimestampRead(t *testing.T, file *os.File) (time.Time, time.Time) {
	t.Helper()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		t.Fatal(err)
	}
	timestamp := func(value windows.Filetime) time.Time {
		ticks := uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
		return time.Unix(int64(ticks/10000000)-11644473600, int64(ticks%10000000)*100)
	}
	return timestamp(info.LastAccessTime), timestamp(info.LastWriteTime)
}
