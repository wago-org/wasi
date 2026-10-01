package core

import (
	"github.com/wago-org/wago"
	"reflect"
	"testing"
)

type referenceBinding struct {
	name            string
	handler         handlerID
	params, results []wago.ValType
	cap             wago.Capability
	docs            string
}

var referenceBindings = func() []referenceBinding {
	i32 := []wago.ValType{wago.ValI32}
	i32x2 := []wago.ValType{wago.ValI32, wago.ValI32}
	i32x3 := []wago.ValType{wago.ValI32, wago.ValI32, wago.ValI32}
	i32x4 := []wago.ValType{wago.ValI32, wago.ValI32, wago.ValI32, wago.ValI32}
	i64 := wago.ValI64
	i32v := wago.ValI32

	return []referenceBinding{
		{"fd_write", dispatchfdWrite, i32x4, i32, CapFDWrite, "write iovecs to a file descriptor (stdout/stderr)"},
		{"fd_read", dispatchfdRead, i32x4, i32, CapFDRead, "read into iovecs from a file descriptor (stdin)"},
		{"fd_close", dispatchfdClose, i32, i32, CapFDManage, "close a file descriptor (streams: no-op)"},
		{"fd_seek", dispatchfdSeek, []wago.ValType{i32v, i64, i32v, i32v}, i32, CapFDManage, "seek a file descriptor (streams: ESPIPE)"},
		{"fd_fdstat_get", dispatchfdFdstatGet, i32x2, i32, CapFDManage, "report fd stat (streams: character device)"},
		{"fd_prestat_get", dispatchfdPrestatGet, i32x2, i32, CapFDManage, "report a preopen (none: EBADF)"},
		{"fd_prestat_dir_name", dispatchfdPrestatDirName, i32x3, i32, CapFDManage, "report a preopen dir name (none: EBADF)"},
		{"proc_exit", dispatchprocExit, i32, nil, CapProcessExit, "terminate the program with an exit code"},
		{"args_sizes_get", dispatchargsSizesGet, i32x2, i32, CapArgumentsRead, "report argc and argv byte size"},
		{"args_get", dispatchargsGet, i32x2, i32, CapArgumentsRead, "write argv pointers and bytes"},
		{"environ_sizes_get", dispatchenvironSizesGet, i32x2, i32, CapEnvironmentRead, "report environ count and byte size"},
		{"environ_get", dispatchenvironGet, i32x2, i32, CapEnvironmentRead, "write environ pointers and bytes"},
		{"clock_time_get", dispatchclockTimeGet, []wago.ValType{i32v, i64, i32v}, i32, CapClockRead, "read a clock's current time"},
		{"clock_res_get", dispatchclockResGet, i32x2, i32, CapClockRead, "read a clock's resolution"},
		{"random_get", dispatchrandomGet, i32x2, i32, CapRandomRead, "fill a buffer with random bytes"},

		{"sched_yield", dispatchschedYield, nil, i32, CapSchedulerYield, "yield execution"},
		{"fd_advise", dispatchfdAdvise, []wago.ValType{i32v, i64, i64, i32v}, i32, CapFDManage, "provide file access advice"},
		{"fd_allocate", dispatchfdAllocate, []wago.ValType{i32v, i64, i64}, i32, CapFDWrite, "allocate file space"},
		{"fd_datasync", dispatchfdDatasync, i32, i32, CapFDWrite, "synchronize file data"},
		{"fd_sync", dispatchfdSync, i32, i32, CapFDWrite, "synchronize a file"},
		{"fd_fdstat_set_flags", dispatchfdFdstatSetFlags, i32x2, i32, CapFDManage, "set descriptor flags"},
		{"fd_fdstat_set_rights", dispatchfdFdstatSetRights, []wago.ValType{i32v, i64, i64}, i32, CapFDManage, "reduce descriptor rights"},
		{"fd_filestat_get", dispatchfdFilestatGet, i32x2, i32, CapFDRead, "get file metadata"},
		{"fd_filestat_set_size", dispatchfdFilestatSetSize, []wago.ValType{i32v, i64}, i32, CapFDWrite, "set file size"},
		{"fd_filestat_set_times", dispatchfdFilestatSetTimes, []wago.ValType{i32v, i64, i64, i32v}, i32, CapFDWrite, "set file timestamps"},
		{"fd_pread", dispatchfdPread, []wago.ValType{i32v, i32v, i32v, i64, i32v}, i32, CapFDRead, "read at an offset"},
		{"fd_pwrite", dispatchfdPwrite, []wago.ValType{i32v, i32v, i32v, i64, i32v}, i32, CapFDWrite, "write at an offset"},
		{"fd_readdir", dispatchfdReaddir, []wago.ValType{i32v, i32v, i32v, i64, i32v}, i32, CapFDRead, "read directory entries"},
		{"fd_renumber", dispatchfdRenumber, i32x2, i32, CapFDManage, "renumber a descriptor"},
		{"fd_tell", dispatchfdTell, i32x2, i32, CapFDManage, "get a descriptor offset"},
		{"path_create_directory", dispatchpathCreateDirectory, i32x3, i32, CapPathWrite, "create a directory"},
		{"path_filestat_get", dispatchpathFilestatGet, []wago.ValType{i32v, i32v, i32v, i32v, i32v}, i32, CapPathRead, "get path metadata"},
		{"path_filestat_set_times", dispatchpathFilestatSetTimes, []wago.ValType{i32v, i32v, i32v, i32v, i64, i64, i32v}, i32, CapPathWrite, "set path timestamps"},
		{"path_link", dispatchpathLink, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v, i32v}, i32, CapPathWrite, "create a hard link"},
		{"path_open", dispatchpathOpen, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i64, i64, i32v, i32v}, i32, CapPathOpen, "open a path with rights limited by its preopen"},
		{"path_readlink", dispatchpathReadlink, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v}, i32, CapPathRead, "read a symbolic link"},
		{"path_remove_directory", dispatchpathRemoveDirectory, i32x3, i32, CapPathWrite, "remove a directory"},
		{"path_rename", dispatchpathRename, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v}, i32, CapPathWrite, "rename a path"},
		{"path_symlink", dispatchpathSymlink, []wago.ValType{i32v, i32v, i32v, i32v, i32v}, i32, CapPathWrite, "create a symbolic link"},
		{"path_unlink_file", dispatchpathUnlinkFile, i32x3, i32, CapPathWrite, "unlink a file"},
		{"poll_oneoff", dispatchpollOneoff, i32x4, i32, CapPoll, "wait for events"},
		{"proc_raise", dispatchprocRaise, i32, i32, CapUnsupported, "raise a signal (unsupported)"},
		{"sock_accept", dispatchsockAccept, i32x3, i32, CapUnsupported, "accept a socket (unsupported)"},
		{"sock_recv", dispatchsockRecv, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v}, i32, CapUnsupported, "receive from a socket (unsupported)"},
		{"sock_send", dispatchsockSend, []wago.ValType{i32v, i32v, i32v, i32v, i32v}, i32, CapUnsupported, "send to a socket (unsupported)"},
		{"sock_shutdown", dispatchsockShutdown, i32x2, i32, CapUnsupported, "shut down a socket (unsupported)"},
	}
}()

func TestFixedDefinitionParity(t *testing.T) {
	if len(importBindings) != 46 || len(referenceBindings) != 46 {
		t.Fatal("definition count")
	}
	for i, want := range referenceBindings {
		got := importBindings[i]
		if got.name != want.name || got.handler != want.handler || got.cap != want.cap || got.docs != want.docs || !reflect.DeepEqual(got.params(), want.params) || !reflect.DeepEqual(got.results(), want.results) {
			t.Fatalf("definition %d changed: %s", i, want.name)
		}
	}
}
