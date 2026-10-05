//go:build linux || darwin

package p2

import (
	"os"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
	"golang.org/x/sys/unix"
)

func nativeTimestampControl(file *os.File, access, modification time.Time) error {
	at, err := unix.TimeToTimespec(access)
	if err != nil {
		return err
	}
	mt, err := unix.TimeToTimespec(modification)
	if err != nil {
		return err
	}
	times := [2]unix.Timespec{at, mt}
	return unix.UtimesNanoAt(unix.AT_FDCWD, file.Name(), times[:], 0)
}

func nativeTimestampRead(t *testing.T, file *os.File) (time.Time, time.Time) {
	t.Helper()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	_, at, mt, _, _, _ := hostStat(info)
	return at, mt
}

func TestDescriptorTimesNoChangeAvoidsHostMutation(t *testing.T) {
	file := timestampDescriptorForTest(t, false)
	want := time.Date(2008, 9, 10, 11, 12, 13, 123456789, time.UTC)
	requireNativeTimestampControl(t, file, want, want)
	before, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, beforeChange, _, _ := hostStat(before)
	// Ensure a mutating syscall gets a distinct ctime even on coarser hosts.
	time.Sleep(10 * time.Millisecond)
	noChange := component.VariantValue{Disc: 0}
	if err := setDescriptorTimes(file, noChange, noChange, time.Now); err != nil {
		t.Fatal(err)
	}
	after, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	_, at, mt, afterChange, _, _ := hostStat(after)
	if !at.Equal(want) || !mt.Equal(want) || !afterChange.Equal(beforeChange) {
		t.Fatalf("no-change altered access/modification/ctime: %v/%v/%v, want %v/%v/%v", at, mt, afterChange, want, want, beforeChange)
	}
}

func TestDescriptorTimesPreservesUnixNanoseconds(t *testing.T) {
	file := timestampDescriptorForTest(t, false)
	want := time.Date(2008, 9, 10, 11, 12, 13, 123456789, time.UTC)
	requireNativeTimestampControl(t, file, want, want)
	if err := setDescriptorTimes(file, timestampForTest(want), timestampForTest(want), time.Now); err != nil {
		t.Fatal(err)
	}
	at, mt := nativeTimestampRead(t, file)
	if !at.Equal(want) || !mt.Equal(want) {
		t.Fatalf("descriptor lost Unix nanoseconds: %v/%v, want %v", at, mt, want)
	}
}
