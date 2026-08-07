// Package core is the shared implementation behind the versioned WASI plugins.
// The command and filesystem surface is shared across wasi_unstable
// (pre-preview1) and wasi_snapshot_preview1; only the wasm
// import module name and extension identity differ, so both wrap this package with
// their own module string. It is internal: use the plugins/wasi/p1 or
// plugins/wasi/unstable wrappers.
package core

import (
	"crypto/rand"
	"encoding/binary"
	"io"

	wago "github.com/wago-org/wago"
)

// Cap is the capability guarding the whole WASI surface. A policy can allow or
// deny it; with no policy it is permitted.
const Cap wago.Capability = "wasi"

// WASI errno values (subset used here); identical across snapshots.
const (
	wasiOK      = 0
	wasiEBadf   = 8
	wasiEInval  = 28
	wasiESpipe  = 70
	wasiENotsup = 58
)

// Config configures the WASI host bundle. A nil writer/reader discards/EOFs; a
// nil Now yields a fixed clock (handy for deterministic tests); a nil Rand uses
// crypto/rand.
type Config struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	Args           []string     // argv; Args[0] is conventionally the program name
	Env            []string     // "KEY=VALUE" entries
	Now            func() int64 // wall-clock nanoseconds for clock_time_get
	Rand           io.Reader    // random source for random_get
	// Preopens maps guest-visible directory names (commonly "/") to host
	// directories. Each entry is exposed as a capability-scoped preopen starting
	// at fd 3. No host filesystem is visible when this is nil.
	Preopens map[string]string
}

// Extension is a WASI extension bound to one wasm import module name. p1 and
// unstable construct it with their own module string and identity. It implements
// wago.Extension.
type Extension struct {
	module string
	info   wago.ExtensionInfo
	cfg    Config
	fs     *fsState
	guard  *fsGuard
}

// New builds a WASI extension that binds its imports under module, identifying
// itself with info.
func New(module string, info wago.ExtensionInfo, cfg Config) *Extension {
	e := &Extension{module: module, info: info, cfg: cfg}
	e.initFS()
	return e
}

// Imports returns the host bundle for module on the low-level
// wago.Instantiate(c, imports) path, keyed "<module>.<name>".
func Imports(module string, cfg Config) wago.Imports {
	return New(module, wago.ExtensionInfo{}, cfg).Imports()
}

// Info identifies the extension.
func (e *Extension) Info() wago.ExtensionInfo { return e.info }

// Register wires the host imports onto reg under the extension's module name.
func (e *Extension) Register(reg *wago.Registry) error {
	// Manifest-loaded WASI gets the current command's argv through the scoped
	// host environment. An explicitly configured argv remains authoritative for
	// the programmatic API.
	env, err := reg.HostEnvironment()
	if err != nil {
		return err
	}
	if e.cfg.Args == nil {
		e.cfg.Args = env.GuestArgs()
	}
	imports, err := reg.HostImports()
	if err != nil {
		return err
	}
	e.guard.resolver = imports.CallerResolver()
	reg.Capability(Cap, wago.CapabilityDocs("wasi: stdio, args/env, clock, random, process exit"))
	m := imports.Module(e.module)
	for _, b := range e.bindings() {
		m.Func(b.name, b.fn).Params(b.params...).Results(b.results...).Capability(Cap).Docs(b.docs)
	}
	return nil
}

// Imports returns the host bundle for the low-level wago.Instantiate(c, imports)
// path, keyed "<module>.<name>".
func (e *Extension) Imports() wago.Imports {
	out := make(wago.Imports)
	for _, b := range e.bindings() {
		out[e.module+"."+b.name] = b.fn
	}
	return out
}

// binding is one host function with its declared signature and docs. Register and
// Imports both derive from bindings so the plugin and raw-bundle paths never drift.
type binding struct {
	name            string
	fn              wago.HostFunc
	params, results []wago.ValType
	docs            string
}

func (e *Extension) bindings() []binding {
	i32 := []wago.ValType{wago.ValI32}
	i32x2 := []wago.ValType{wago.ValI32, wago.ValI32}
	i32x3 := []wago.ValType{wago.ValI32, wago.ValI32, wago.ValI32}
	i32x4 := []wago.ValType{wago.ValI32, wago.ValI32, wago.ValI32, wago.ValI32}
	i64 := wago.ValI64
	i32v := wago.ValI32

	bindings := []binding{
		{"fd_write", e.fdWrite, i32x4, i32, "write iovecs to a file descriptor (stdout/stderr)"},
		{"fd_read", e.fdRead, i32x4, i32, "read into iovecs from a file descriptor (stdin)"},
		{"fd_close", e.fdClose, i32, i32, "close a file descriptor (streams: no-op)"},
		{"fd_seek", e.fdSeek, []wago.ValType{i32v, i64, i32v, i32v}, i32, "seek a file descriptor (streams: ESPIPE)"},
		{"fd_fdstat_get", e.fdFdstatGet, i32x2, i32, "report fd stat (streams: character device)"},
		{"fd_prestat_get", e.fdPrestatGet, i32x2, i32, "report a preopen (none: EBADF)"},
		{"fd_prestat_dir_name", e.fdPrestatDirName, i32x3, i32, "report a preopen dir name (none: EBADF)"},
		{"proc_exit", e.procExit, i32, nil, "terminate the program with an exit code"},
		{"args_sizes_get", e.argsSizesGet, i32x2, i32, "report argc and argv byte size"},
		{"args_get", e.argsGet, i32x2, i32, "write argv pointers and bytes"},
		{"environ_sizes_get", e.environSizesGet, i32x2, i32, "report environ count and byte size"},
		{"environ_get", e.environGet, i32x2, i32, "write environ pointers and bytes"},
		{"clock_time_get", e.clockTimeGet, []wago.ValType{i32v, i64, i32v}, i32, "read a clock's current time"},
		{"clock_res_get", e.clockResGet, i32x2, i32, "read a clock's resolution"},
		{"random_get", e.randomGet, i32x2, i32, "fill a buffer with random bytes"},

		{"sched_yield", e.schedYield, nil, i32, "yield execution"},
		{"fd_advise", e.fdAdvise, []wago.ValType{i32v, i64, i64, i32v}, i32, "provide file access advice"},
		{"fd_allocate", e.fdAllocate, []wago.ValType{i32v, i64, i64}, i32, "allocate file space"},
		{"fd_datasync", e.fdDatasync, i32, i32, "synchronize file data"},
		{"fd_sync", e.fdSync, i32, i32, "synchronize a file"},
		{"fd_fdstat_set_flags", e.fdFdstatSetFlags, i32x2, i32, "set descriptor flags"},
		{"fd_fdstat_set_rights", e.fdFdstatSetRights, []wago.ValType{i32v, i64, i64}, i32, "reduce descriptor rights"},
		{"fd_filestat_get", e.fdFilestatGet, i32x2, i32, "get file metadata"},
		{"fd_filestat_set_size", e.fdFilestatSetSize, []wago.ValType{i32v, i64}, i32, "set file size"},
		{"fd_filestat_set_times", e.fdFilestatSetTimes, []wago.ValType{i32v, i64, i64, i32v}, i32, "set file timestamps"},
		{"fd_pread", e.fdPread, []wago.ValType{i32v, i32v, i32v, i64, i32v}, i32, "read at an offset"},
		{"fd_pwrite", e.fdPwrite, []wago.ValType{i32v, i32v, i32v, i64, i32v}, i32, "write at an offset"},
		{"fd_readdir", e.fdReaddir, []wago.ValType{i32v, i32v, i32v, i64, i32v}, i32, "read directory entries"},
		{"fd_renumber", e.fdRenumber, i32x2, i32, "renumber a descriptor"},
		{"fd_tell", e.fdTell, i32x2, i32, "get a descriptor offset"},
		{"path_create_directory", e.pathCreateDirectory, i32x3, i32, "create a directory"},
		{"path_filestat_get", e.pathFilestatGet, []wago.ValType{i32v, i32v, i32v, i32v, i32v}, i32, "get path metadata"},
		{"path_filestat_set_times", e.pathFilestatSetTimes, []wago.ValType{i32v, i32v, i32v, i32v, i64, i64, i32v}, i32, "set path timestamps"},
		{"path_link", e.pathLink, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v, i32v}, i32, "create a hard link"},
		{"path_open", e.pathOpen, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i64, i64, i32v, i32v}, i32, "open a path"},
		{"path_readlink", e.pathReadlink, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v}, i32, "read a symbolic link"},
		{"path_remove_directory", e.pathRemoveDirectory, i32x3, i32, "remove a directory"},
		{"path_rename", e.pathRename, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v}, i32, "rename a path"},
		{"path_symlink", e.pathSymlink, []wago.ValType{i32v, i32v, i32v, i32v, i32v}, i32, "create a symbolic link"},
		{"path_unlink_file", e.pathUnlinkFile, i32x3, i32, "unlink a file"},
		{"poll_oneoff", e.pollOneoff, i32x4, i32, "wait for events"},
		{"proc_raise", e.procRaise, i32, i32, "raise a signal"},
		{"sock_accept", e.sockAccept, i32x3, i32, "accept a socket"},
		{"sock_recv", e.sockRecv, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v}, i32, "receive from a socket"},
		{"sock_send", e.sockSend, []wago.ValType{i32v, i32v, i32v, i32v, i32v}, i32, "send to a socket"},
		{"sock_shutdown", e.sockShutdown, i32x2, i32, "shut down a socket"},
	}
	for i := range bindings {
		fn := bindings[i].fn
		bindings[i].fn = func(m wago.HostModule, p, r []uint64) {
			e.withFS(m, func() { fn(m, p, r) })
		}
	}
	return bindings
}

// --- memory helpers (bounds-checked; malformed pointers yield EFAULT, never a
// Go panic that would abort the whole instance) ---

func le32(mem []byte, off uint32) (uint32, bool) {
	if int(off)+4 > len(mem) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(mem[off:]), true
}

func putLe32(mem []byte, off, v uint32) bool {
	if int(off)+4 > len(mem) {
		return false
	}
	binary.LittleEndian.PutUint32(mem[off:], v)
	return true
}

func putLe64(mem []byte, off uint32, v uint64) bool {
	if int(off)+8 > len(mem) {
		return false
	}
	binary.LittleEndian.PutUint64(mem[off:], v)
	return true
}

// --- fd_* ---

func (e *Extension) fdWrite(m wago.HostModule, p, r []uint64) {
	fd, iovs, n, nwrittenPtr := uint32(p[0]), uint32(p[1]), uint32(p[2]), uint32(p[3])
	var out io.Writer
	switch fd {
	case 1:
		out = e.cfg.Stdout
	case 2:
		out = e.cfg.Stderr
	default:
		f, code := e.entry(fd)
		if code != 0 {
			r[0] = code
			return
		}
		if code = require(f, rightFDWrite); code != 0 {
			r[0] = code
			return
		}
		if f.file == nil {
			r[0] = wasiEBadf
			return
		}
		out = f.file
	}
	mem := m.Memory()
	bufs, code := iovecs(mem, iovs, n)
	if code != 0 {
		r[0] = code
		return
	}
	var total uint32
	for _, buf := range bufs {
		if out != nil {
			nn, err := out.Write(buf)
			total += uint32(nn)
			if err != nil {
				r[0] = errno(err)
				return
			}
		} else {
			total += uint32(len(buf))
		}
	}
	if !putLe32(mem, nwrittenPtr, total) {
		r[0] = wasiEFault
		return
	}
	r[0] = wasiOK
}

func (e *Extension) fdRead(m wago.HostModule, p, r []uint64) {
	fd, iovs, n, nreadPtr := uint32(p[0]), uint32(p[1]), uint32(p[2]), uint32(p[3])
	var in io.Reader
	if fd == 0 {
		in = e.cfg.Stdin
		if in == nil { // stdin with no reader: clean EOF
			if putLe32(m.Memory(), nreadPtr, 0) {
				r[0] = wasiOK
				return
			}
			r[0] = wasiEFault
			return
		}
	} else {
		f, code := e.entry(fd)
		if code != 0 {
			r[0] = code
			return
		}
		if code = require(f, rightFDRead); code != 0 {
			r[0] = code
			return
		}
		if f.file == nil {
			r[0] = wasiEBadf
			return
		}
		in = f.file
	}
	mem := m.Memory()
	bufs, code := iovecs(mem, iovs, n)
	if code != 0 {
		r[0] = code
		return
	}
	var total uint32
	for _, buf := range bufs {
		nn, err := in.Read(buf)
		total += uint32(nn)
		if err != nil || nn < len(buf) {
			break
		}
	}
	if !putLe32(mem, nreadPtr, total) {
		r[0] = wasiEFault
		return
	}
	r[0] = wasiOK
}

func (e *Extension) fdClose(_ wago.HostModule, p, r []uint64) {
	fd := uint32(p[0])
	f, code := e.entry(fd)
	if code == 0 {
		if f.file != nil {
			code = errno(f.file.Close())
		}
		if code == 0 {
			delete(e.fs.fds, fd)
		}
	}
	r[0] = code
}

func (e *Extension) fdSeek(m wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDSeek)
	}
	if code == 0 && f.file == nil {
		code = wasiESpipe
	}
	if code == 0 {
		if st, err := f.file.Stat(); err != nil {
			code = errno(err)
		} else if st.IsDir() {
			code = wasiEBadf
		}
	}
	whence := int(p[2])
	if code == 0 && (whence < 0 || whence > 2) {
		code = wasiEInval
	}
	if code == 0 {
		off, err := f.file.Seek(int64(p[1]), whence)
		if err != nil {
			code = errno(err)
		} else if !putLe64(m.Memory(), uint32(p[3]), uint64(off)) {
			code = wasiEFault
		}
	}
	r[0] = code
}

func (e *Extension) fdFdstatGet(m wago.HostModule, p, r []uint64) {
	fd, buf := uint32(p[0]), uint32(p[1])
	f, code := e.entry(fd)
	if code != 0 {
		r[0] = code
		return
	}
	mem := m.Memory()
	if int(buf)+24 > len(mem) {
		r[0] = wasiEFault
		return
	}
	for i := uint32(0); i < 24; i++ {
		mem[buf+i] = 0
	}
	if f.file == nil {
		mem[buf] = filetypeCharacterDevice
	} else if st, err := f.file.Stat(); err != nil {
		r[0] = errno(err)
		return
	} else {
		mem[buf] = filetype(st)
	}
	binary.LittleEndian.PutUint16(mem[buf+2:], f.flags)
	binary.LittleEndian.PutUint64(mem[buf+8:], f.rights)
	binary.LittleEndian.PutUint64(mem[buf+16:], f.inheriting)
	r[0] = wasiOK
}

func (e *Extension) fdPrestatGet(m wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 && f.preopen == "" {
		code = wasiEBadf
	}
	if code == 0 {
		mem, ptr := m.Memory(), uint32(p[1])
		if uint64(ptr)+8 > uint64(len(mem)) {
			code = wasiEFault
		} else {
			clear(mem[ptr : ptr+8])
			binary.LittleEndian.PutUint32(mem[ptr+4:], uint32(len(f.preopen)))
		}
	}
	r[0] = code
}

func (e *Extension) fdPrestatDirName(m wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 && f.preopen == "" {
		code = wasiEBadf
	}
	ptr, n := uint32(p[1]), uint32(p[2])
	if code == 0 && n < uint32(len(f.preopen)) {
		code = wasiENametoolong
	}
	if code == 0 && uint64(ptr)+uint64(len(f.preopen)) > uint64(len(m.Memory())) {
		code = wasiEFault
	}
	if code == 0 {
		copy(m.Memory()[ptr:], f.preopen)
	}
	r[0] = code
}

// --- process / args / env ---

func (e *Extension) procExit(_ wago.HostModule, p, r []uint64) {
	panic(wago.HostExit{Code: int32(uint32(p[0]))})
}

func (e *Extension) argsSizesGet(m wago.HostModule, p, r []uint64) {
	r[0] = writeCounts(m.Memory(), uint32(p[0]), uint32(p[1]), e.cfg.Args)
}

func (e *Extension) argsGet(m wago.HostModule, p, r []uint64) {
	r[0] = writeStrings(m.Memory(), uint32(p[0]), uint32(p[1]), e.cfg.Args)
}

func (e *Extension) environSizesGet(m wago.HostModule, p, r []uint64) {
	r[0] = writeCounts(m.Memory(), uint32(p[0]), uint32(p[1]), e.cfg.Env)
}

func (e *Extension) environGet(m wago.HostModule, p, r []uint64) {
	r[0] = writeStrings(m.Memory(), uint32(p[0]), uint32(p[1]), e.cfg.Env)
}

// writeCounts writes the item count and the total NUL-terminated byte size.
func writeCounts(mem []byte, countPtr, sizePtr uint32, items []string) uint64 {
	total := 0
	for _, s := range items {
		total += len(s) + 1
	}
	if !putLe32(mem, countPtr, uint32(len(items))) || !putLe32(mem, sizePtr, uint32(total)) {
		return wasiEFault
	}
	return wasiOK
}

// writeStrings writes the pointer array then the packed NUL-terminated strings.
func writeStrings(mem []byte, ptrArray, buf uint32, items []string) uint64 {
	cur := buf
	for i, s := range items {
		if !putLe32(mem, ptrArray+uint32(i)*4, cur) {
			return wasiEFault
		}
		if int(cur)+len(s)+1 > len(mem) {
			return wasiEFault
		}
		copy(mem[cur:], s)
		mem[cur+uint32(len(s))] = 0
		cur += uint32(len(s)) + 1
	}
	return wasiOK
}

// --- clock / random ---

func (e *Extension) clockTimeGet(m wago.HostModule, p, r []uint64) {
	if p[0] > 3 {
		r[0] = wasiEInval
		return
	}
	var now int64
	if e.cfg.Now != nil {
		now = e.cfg.Now()
	}
	if !putLe64(m.Memory(), uint32(p[2]), uint64(now)) {
		r[0] = wasiEFault
		return
	}
	r[0] = wasiOK
}

// clockResGet writes a coarse clock resolution (1ns) and succeeds.
func (e *Extension) clockResGet(m wago.HostModule, p, r []uint64) {
	if p[0] > 3 {
		r[0] = wasiEInval
		return
	}
	if !putLe64(m.Memory(), uint32(p[1]), 1) {
		r[0] = wasiEFault
		return
	}
	r[0] = wasiOK
}

func (e *Extension) randomGet(m wago.HostModule, p, r []uint64) {
	buf, n := uint32(p[0]), uint32(p[1])
	mem := m.Memory()
	if int(buf)+int(n) > len(mem) {
		r[0] = wasiEFault
		return
	}
	src := e.cfg.Rand
	if src == nil {
		src = rand.Reader
	}
	if _, err := io.ReadFull(src, mem[buf:buf+n]); err != nil {
		r[0] = wasiEIo
		return
	}
	r[0] = wasiOK
}
