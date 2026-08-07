package core

import (
	"encoding/binary"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	wago "github.com/wago-org/wago"
	"golang.org/x/sys/unix"
)

const maxDirectoryEntries = 16384
const maxInt64Value = uint64(^uint64(0) >> 1)

type fsState struct {
	fds    map[uint32]*fdEntry
	nextFD uint32
	maxFDs uint32
}

type fsGuard struct {
	mu       sync.Mutex
	resolver *wago.CallerResolver
	states   map[*wago.Instance]*fsState
	raw      map[wago.HostModule]*fsState
	claimed  bool
}

type fdEntry struct {
	file       *os.File
	mount      string
	preopen    string
	flags      uint16
	rights     uint64
	inheriting uint64
	dirCookies map[uint64]bool
}

func (e *Extension) initFS() {
	e.guard = &fsGuard{states: make(map[*wago.Instance]*fsState), raw: make(map[wago.HostModule]*fsState)}
	e.fs = e.makeFS()
}

func (e *Extension) makeFS() *fsState {
	maxFDs := e.cfg.MaxOpenFiles
	if maxFDs == 0 {
		maxFDs = 1024
	}
	if maxFDs < 3 {
		maxFDs = 3
	}
	s := &fsState{fds: make(map[uint32]*fdEntry), nextFD: 3, maxFDs: maxFDs}
	for fd := uint32(0); fd < 3; fd++ {
		rights := rightFDFilestatGet | rightPollFDReadWrite
		if fd == 0 {
			rights |= rightFDRead
		} else {
			rights |= rightFDWrite
		}
		s.fds[fd] = &fdEntry{rights: rights}
	}
	names := make([]string, 0, len(e.cfg.Preopens))
	for name := range e.cfg.Preopens {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if uint32(len(s.fds)) >= maxFDs {
			break
		}
		host, err := filepath.Abs(e.cfg.Preopens[name])
		if err != nil {
			continue
		}
		f, err := os.Open(host)
		if err != nil {
			continue
		}
		fd := s.nextFD
		s.nextFD++
		s.fds[fd] = &fdEntry{file: f, mount: host, preopen: name, rights: directoryRights, inheriting: allRights, dirCookies: map[uint64]bool{0: true}}
	}
	return s
}

func (e *Extension) withFS(m wago.HostModule, call func()) {
	e.guard.mu.Lock()
	defer e.guard.mu.Unlock()
	state := e.fs
	if e.guard.resolver != nil {
		if in, err := e.guard.resolver.Resolve(m); err == nil {
			state = e.guard.states[in]
			if state == nil {
				if !e.guard.claimed {
					state = e.fs
					e.guard.claimed = true
				} else {
					state = e.makeFS()
				}
				e.guard.states[in] = state
			}
		}
	} else if m != nil {
		state = e.guard.raw[m]
		if state == nil {
			if !e.guard.claimed {
				state = e.fs
				e.guard.claimed = true
			} else {
				state = e.makeFS()
			}
			e.guard.raw[m] = state
		}
	}
	previous := e.fs
	e.fs = state
	defer func() { e.fs = previous }()
	call()
}

func closeFS(state *fsState) {
	if state == nil {
		return
	}
	for _, entry := range state.fds {
		if entry.file != nil {
			_ = entry.file.Close()
		}
	}
	clear(state.fds)
}

func (e *Extension) closeInstance(in *wago.Instance) {
	e.guard.mu.Lock()
	defer e.guard.mu.Unlock()
	state := e.guard.states[in]
	delete(e.guard.states, in)
	closeFS(state)
}

func (e *Extension) entry(fd uint32) (*fdEntry, uint64) {
	f := e.fs.fds[fd]
	if f == nil {
		return nil, wasiEBadf
	}
	return f, wasiOK
}

func require(f *fdEntry, right uint64) uint64 {
	if f.rights&right != right {
		return wasiENotcapable
	}
	return wasiOK
}

func guestBytes(mem []byte, ptr, n uint32) (string, uint64) {
	if uint64(ptr)+uint64(n) > uint64(len(mem)) {
		return "", wasiEFault
	}
	b := mem[ptr : ptr+n]
	if strings.IndexByte(string(b), 0) >= 0 {
		return "", wasiEInval
	}
	return string(b), wasiOK
}

// resolve validates a guest path and returns its directory capability. Kernel
// operations below additionally enforce RESOLVE_BENEATH so validation and use
// cannot be separated by a symlink race.
func (e *Extension) resolve(fd uint32, guest string) (*fdEntry, string, uint64) {
	d, code := e.entry(fd)
	if code != 0 {
		return nil, "", code
	}
	if d.file == nil {
		return nil, "", wasiENotdir
	}
	st, err := d.file.Stat()
	if err != nil {
		return nil, "", errno(err)
	}
	if !st.IsDir() {
		return nil, "", wasiENotdir
	}
	if guest == "" || strings.HasPrefix(guest, "/") {
		return nil, "", wasiENotcapable
	}
	depth := 0
	for _, part := range strings.Split(guest, "/") {
		switch part {
		case "", ".":
		case "..":
			depth--
		default:
			depth++
		}
		if depth < 0 {
			return nil, "", wasiENotcapable
		}
	}
	return d, path.Clean(guest), wasiOK
}

const secureResolve = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS

func capabilityErr(err error) uint64 {
	if err == syscall.EXDEV {
		return wasiENotcapable
	}
	return errno(err)
}

func openAt(d *fdEntry, name string, flags int, mode uint32) (*os.File, uint64) {
	if flags&unix.O_CREAT == 0 {
		mode = 0
	}
	fd, err := unix.Openat2(int(d.file.Fd()), name, &unix.OpenHow{
		Flags: uint64(flags | unix.O_CLOEXEC), Mode: uint64(mode), Resolve: secureResolve,
	})
	if err != nil {
		return nil, capabilityErr(err)
	}
	return os.NewFile(uintptr(fd), name), wasiOK
}

func openParent(d *fdEntry, name string) (*os.File, string, uint64) {
	parent, leaf := path.Split(name)
	parent = strings.TrimSuffix(parent, "/")
	if parent == "" {
		parent = "."
	}
	f, code := openAt(d, parent, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	return f, leaf, code
}

func (e *Extension) alloc(entry *fdEntry) (uint32, uint64) {
	if uint32(len(e.fs.fds)) >= e.fs.maxFDs {
		return 0, wasiEMfile
	}
	fd := e.fs.nextFD
	for e.fs.fds[fd] != nil {
		fd++
	}
	e.fs.nextFD = fd + 1
	e.fs.fds[fd] = entry
	return fd, wasiOK
}

func iovecs(mem []byte, ptr, count uint32) ([][]byte, uint64) {
	if uint64(ptr)+uint64(count)*8 > uint64(len(mem)) {
		return nil, wasiEFault
	}
	bufs := make([][]byte, 0, count)
	for i := uint32(0); i < count; i++ {
		base := binary.LittleEndian.Uint32(mem[ptr+i*8:])
		n := binary.LittleEndian.Uint32(mem[ptr+i*8+4:])
		if uint64(base)+uint64(n) > uint64(len(mem)) {
			return nil, wasiEFault
		}
		bufs = append(bufs, mem[base:base+n])
	}
	return bufs, wasiOK
}

func writeFilestat(mem []byte, ptr uint32, info os.FileInfo) uint64 {
	if uint64(ptr)+64 > uint64(len(mem)) {
		return wasiEFault
	}
	b := mem[ptr : ptr+64]
	clear(b)
	var dev, ino, nlink uint64 = 1, 1, 1
	var atim, ctim int64 = info.ModTime().UnixNano(), info.ModTime().UnixNano()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		dev, ino, nlink = uint64(st.Dev), st.Ino, uint64(st.Nlink)
		atim = st.Atim.Sec*1e9 + st.Atim.Nsec
		ctim = st.Ctim.Sec*1e9 + st.Ctim.Nsec
	}
	binary.LittleEndian.PutUint64(b[0:], dev)
	binary.LittleEndian.PutUint64(b[8:], ino)
	b[16] = filetype(info)
	binary.LittleEndian.PutUint64(b[24:], nlink)
	binary.LittleEndian.PutUint64(b[32:], uint64(info.Size()))
	binary.LittleEndian.PutUint64(b[40:], uint64(atim))
	binary.LittleEndian.PutUint64(b[48:], uint64(info.ModTime().UnixNano()))
	binary.LittleEndian.PutUint64(b[56:], uint64(ctim))
	return wasiOK
}

func (e *Extension) fdAdvise(_ wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDAdvise)
	}
	if code == 0 && p[3] > 5 {
		code = wasiEInval
	}
	r[0] = code
}

func (e *Extension) fdAllocate(_ wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDAllocate)
	}
	if code == 0 && f.file == nil {
		code = wasiEBadf
	}
	end := p[1] + p[2]
	if code == 0 && (end < p[1] || end > uint64(^uint64(0)>>1)) {
		code = wasiEInval
	}
	if code == 0 {
		st, err := f.file.Stat()
		if err != nil {
			code = errno(err)
		} else if uint64(st.Size()) < end {
			code = errno(f.file.Truncate(int64(end)))
		}
	}
	r[0] = code
}

func (e *Extension) fdDatasync(_ wago.HostModule, p, r []uint64) {
	e.syncFD(uint32(p[0]), rightFDDataSync, r)
}
func (e *Extension) fdSync(_ wago.HostModule, p, r []uint64) { e.syncFD(uint32(p[0]), rightFDSync, r) }
func (e *Extension) syncFD(fd uint32, right uint64, r []uint64) {
	f, code := e.entry(fd)
	if code == 0 {
		code = require(f, right)
	}
	if code == 0 && f.file != nil {
		code = errno(f.file.Sync())
	}
	r[0] = code
}

func (e *Extension) fdFdstatSetFlags(_ wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	flags := uint16(p[1])
	if code == 0 {
		code = require(f, rightFDStatSetFlags)
	}
	if code == 0 && p[1]&^uint64(0x1f) != 0 {
		code = wasiEInval
	}
	if code == 0 {
		f.flags = flags
		if f.file != nil {
			current, _, callErr := syscall.Syscall(syscall.SYS_FCNTL, f.file.Fd(), syscall.F_GETFL, 0)
			if callErr != 0 {
				code = errno(callErr)
			} else {
				current &^= syscall.O_APPEND | syscall.O_NONBLOCK
				if flags&1 != 0 {
					current |= syscall.O_APPEND
				}
				if flags&4 != 0 {
					current |= syscall.O_NONBLOCK
				}
				_, _, callErr = syscall.Syscall(syscall.SYS_FCNTL, f.file.Fd(), syscall.F_SETFL, current)
				if callErr != 0 {
					code = errno(callErr)
				}
			}
		}
	}
	r[0] = code
}

func (e *Extension) fdFdstatSetRights(_ wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	base, inheriting := p[1], p[2]
	if code == 0 && (base&^f.rights != 0 || inheriting&^f.inheriting != 0) {
		code = wasiENotcapable
	}
	if code == 0 {
		f.rights, f.inheriting = base, inheriting
	}
	r[0] = code
}

func (e *Extension) fdFilestatGet(m wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDFilestatGet)
	}
	if code == 0 && f.file == nil {
		code = wasiEBadf
	}
	if code == 0 {
		st, err := f.file.Stat()
		if err != nil {
			code = errno(err)
		} else {
			code = writeFilestat(m.Memory(), uint32(p[1]), st)
		}
	}
	r[0] = code
}

func (e *Extension) fdFilestatSetSize(_ wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDFilestatSetSize)
	}
	if code == 0 && f.file == nil {
		code = wasiEBadf
	}
	if code == 0 && p[1] > maxInt64Value {
		code = wasiEOverflow
	}
	if code == 0 {
		code = errno(f.file.Truncate(int64(p[1])))
	}
	r[0] = code
}

func validFstFlags(flags uint64) bool {
	return flags&^uint64(15) == 0 && flags&3 != 3 && flags&12 != 12
}

func timesFor(info os.FileInfo, atim, mtim uint64, flags uint64) ([]unix.Timespec, uint64) {
	if !validFstFlags(flags) {
		return nil, wasiEInval
	}
	if flags&1 != 0 && atim > maxInt64Value || flags&4 != 0 && mtim > maxInt64Value {
		return nil, wasiEOverflow
	}
	a, mt := info.ModTime(), info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		a = time.Unix(st.Atim.Sec, st.Atim.Nsec)
	}
	now := time.Now()
	if flags&1 != 0 {
		a = time.Unix(0, int64(atim))
	}
	if flags&2 != 0 {
		a = now
	}
	if flags&4 != 0 {
		mt = time.Unix(0, int64(mtim))
	}
	if flags&8 != 0 {
		mt = now
	}
	return []unix.Timespec{unix.NsecToTimespec(a.UnixNano()), unix.NsecToTimespec(mt.UnixNano())}, wasiOK
}

func (e *Extension) fdFilestatSetTimes(_ wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDFilestatSetTimes)
	}
	if code == 0 && f.file == nil {
		code = wasiEBadf
	}
	if code == 0 {
		st, err := f.file.Stat()
		if err != nil {
			code = errno(err)
		} else {
			times, timeCode := timesFor(st, p[1], p[2], p[3])
			code = timeCode
			if code == 0 {
				code = errno(unix.UtimesNanoAt(int(f.file.Fd()), "", times, unix.AT_EMPTY_PATH))
			}
		}
	}
	r[0] = code
}

func (e *Extension) fdPread(m wago.HostModule, p, r []uint64)  { e.readAt(m, p, r) }
func (e *Extension) fdPwrite(m wago.HostModule, p, r []uint64) { e.writeAt(m, p, r) }

func (e *Extension) readAt(m wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDRead|rightFDSeek)
	}
	bufs, memCode := iovecs(m.Memory(), uint32(p[1]), uint32(p[2]))
	if code == 0 {
		code = memCode
	}
	var total uint32
	if code == 0 && p[3] > maxInt64Value {
		code = wasiEOverflow
	}
	off := int64(p[3])
	if code == 0 && f.file == nil {
		code = wasiEBadf
	}
	for _, b := range bufs {
		if code != 0 {
			break
		}
		n, err := f.file.ReadAt(b, off)
		total += uint32(n)
		off += int64(n)
		if err != nil && err != io.EOF {
			code = errno(err)
		}
		if n < len(b) {
			break
		}
	}
	if code == 0 && !putLe32(m.Memory(), uint32(p[4]), total) {
		code = wasiEFault
	}
	r[0] = code
}

func (e *Extension) writeAt(m wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDWrite|rightFDSeek)
	}
	bufs, memCode := iovecs(m.Memory(), uint32(p[1]), uint32(p[2]))
	if code == 0 {
		code = memCode
	}
	var total uint32
	if code == 0 && p[3] > maxInt64Value {
		code = wasiEOverflow
	}
	off := int64(p[3])
	if code == 0 && f.file == nil {
		code = wasiEBadf
	}
	for _, b := range bufs {
		if code != 0 {
			break
		}
		var n int
		var err error
		if f.flags&1 != 0 {
			n, err = unix.Pwrite(int(f.file.Fd()), b, off)
		} else {
			n, err = f.file.WriteAt(b, off)
		}
		total += uint32(n)
		off += int64(n)
		if err != nil {
			code = errno(err)
		}
	}
	if code == 0 && !putLe32(m.Memory(), uint32(p[4]), total) {
		code = wasiEFault
	}
	r[0] = code
}

func (e *Extension) fdReaddir(m wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDReadDir)
	}
	if code == 0 && f.file == nil {
		code = wasiEBadf
	}
	cookie := p[3]
	if code == 0 && (f.dirCookies == nil || !f.dirCookies[cookie]) {
		code = wasiENoent
	}
	var entries []os.DirEntry
	if code == 0 {
		dir, openCode := openAt(f, ".", unix.O_RDONLY|unix.O_DIRECTORY, 0)
		code = openCode
		if code == 0 {
			var err error
			entries, err = dir.ReadDir(maxDirectoryEntries)
			_ = dir.Close()
			if err != nil && err != io.EOF {
				code = errno(err)
			}
		}
	}
	buf, bufLen := uint32(p[1]), uint32(p[2])
	mem := m.Memory()
	if code == 0 && uint64(buf)+uint64(bufLen) > uint64(len(mem)) {
		code = wasiEFault
	}
	var used uint32
	for i := int(cookie); code == 0 && i < len(entries)+2; i++ {
		name := "."
		var info os.FileInfo
		var err error
		if i == 0 {
			info, err = f.file.Stat()
		} else if i == 1 {
			name = ".."
			info, err = f.file.Stat()
		} else {
			name = entries[i-2].Name()
			entryFile, openCode := openAt(f, name, unix.O_PATH|unix.O_NOFOLLOW, 0)
			if openCode != 0 {
				code = openCode
				break
			}
			info, err = entryFile.Stat()
			_ = entryFile.Close()
		}
		if err != nil {
			code = errno(err)
			break
		}
		rec := make([]byte, 24+len(name))
		binary.LittleEndian.PutUint64(rec[0:], uint64(i+1))
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			binary.LittleEndian.PutUint64(rec[8:], st.Ino)
		}
		binary.LittleEndian.PutUint32(rec[16:], uint32(len(name)))
		rec[20] = filetype(info)
		copy(rec[24:], name)
		remaining := int(bufLen - used)
		if remaining <= 0 {
			break
		}
		n := len(rec)
		if n > remaining {
			n = remaining
		}
		copy(mem[buf+used:], rec[:n])
		used += uint32(n)
		if n >= 24 {
			f.dirCookies[uint64(i+1)] = true
		}
		if n < len(rec) {
			break
		}
	}
	if code == 0 && !putLe32(mem, uint32(p[4]), used) {
		code = wasiEFault
	}
	r[0] = code
}

func (e *Extension) fdRenumber(_ wago.HostModule, p, r []uint64) {
	from, to := uint32(p[0]), uint32(p[1])
	f, code := e.entry(from)
	if code == 0 && to > uint32(^uint32(0)>>1) {
		code = wasiEBadf
	}
	if code == 0 && e.fs.fds[to] == nil {
		code = wasiEBadf
	}
	if code == 0 && from == to {
		r[0] = wasiOK
		return
	}
	if code == 0 {
		if old := e.fs.fds[to]; old != nil && old.file != nil {
			_ = old.file.Close()
		}
		e.fs.fds[to] = f
		delete(e.fs.fds, from)
	}
	r[0] = code
}

func (e *Extension) fdTell(m wago.HostModule, p, r []uint64) {
	f, code := e.entry(uint32(p[0]))
	if code == 0 {
		code = require(f, rightFDTell)
	}
	if code == 0 && f.file == nil {
		code = wasiESpipe
	}
	if code == 0 {
		off, err := f.file.Seek(0, io.SeekCurrent)
		if err != nil {
			code = errno(err)
		} else if !putLe64(m.Memory(), uint32(p[1]), uint64(off)) {
			code = wasiEFault
		}
	}
	r[0] = code
}

func (e *Extension) pathCreateDirectory(m wago.HostModule, p, r []uint64) {
	e.pathUnary(m, p, r, rightPathCreateDirectory, func(fd int, name string) error { return unix.Mkdirat(fd, name, 0o777) })
}

func (e *Extension) pathUnary(m wago.HostModule, p, r []uint64, right uint64, op func(int, string) error) {
	name, code := guestBytes(m.Memory(), uint32(p[1]), uint32(p[2]))
	d, name, pathCode := e.resolve(uint32(p[0]), name)
	if code == 0 {
		code = pathCode
	}
	if code == 0 {
		code = require(d, right)
	}
	if code == 0 {
		parent, leaf, parentCode := openParent(d, name)
		code = parentCode
		if code == 0 {
			code = errno(op(int(parent.Fd()), leaf))
			_ = parent.Close()
		}
	}
	r[0] = code
}

func (e *Extension) pathFilestatGet(m wago.HostModule, p, r []uint64) {
	name, code := guestBytes(m.Memory(), uint32(p[2]), uint32(p[3]))
	d, name, pathCode := e.resolve(uint32(p[0]), name)
	if code == 0 {
		code = pathCode
	}
	if code == 0 {
		code = require(d, rightPathFilestatGet)
	}
	if code == 0 && p[1]&^uint64(1) != 0 {
		code = wasiEInval
	}
	var st os.FileInfo
	if code == 0 {
		flags := unix.O_PATH
		if uint16(p[1])&1 == 0 {
			flags |= unix.O_NOFOLLOW
		}
		f, openCode := openAt(d, name, flags, 0)
		code = openCode
		if code == 0 {
			var err error
			st, err = f.Stat()
			_ = f.Close()
			if err != nil {
				code = errno(err)
			}
		}
	}
	if code == 0 {
		code = writeFilestat(m.Memory(), uint32(p[4]), st)
	}
	r[0] = code
}

func (e *Extension) pathFilestatSetTimes(m wago.HostModule, p, r []uint64) {
	name, code := guestBytes(m.Memory(), uint32(p[2]), uint32(p[3]))
	d, name, pathCode := e.resolve(uint32(p[0]), name)
	if code == 0 {
		code = pathCode
	}
	if code == 0 {
		code = require(d, rightPathFilestatSetTimes)
	}
	if code == 0 && p[1]&^uint64(1) != 0 {
		code = wasiEInval
	}
	if code == 0 {
		follow := uint16(p[1])&1 != 0
		if follow {
			f, openCode := openAt(d, name, unix.O_PATH, 0)
			code = openCode
			if code == 0 {
				st, err := f.Stat()
				if err != nil {
					code = errno(err)
				} else {
					times, timeCode := timesFor(st, p[4], p[5], p[6])
					code = timeCode
					if code == 0 {
						code = errno(unix.UtimesNanoAt(int(f.Fd()), "", times, unix.AT_EMPTY_PATH))
					}
				}
				_ = f.Close()
			}
		} else {
			parent, leaf, parentCode := openParent(d, name)
			code = parentCode
			if code == 0 {
				f, openCode := openAt(d, name, unix.O_PATH|unix.O_NOFOLLOW, 0)
				code = openCode
				if code == 0 {
					st, err := f.Stat()
					_ = f.Close()
					if err != nil {
						code = errno(err)
					} else {
						times, timeCode := timesFor(st, p[4], p[5], p[6])
						code = timeCode
						if code == 0 {
							code = errno(unix.UtimesNanoAt(int(parent.Fd()), leaf, times, unix.AT_SYMLINK_NOFOLLOW))
						}
					}
				}
				_ = parent.Close()
			}
		}
	}
	r[0] = code
}

func (e *Extension) pathLink(m wago.HostModule, p, r []uint64) {
	oldName, code := guestBytes(m.Memory(), uint32(p[2]), uint32(p[3]))
	newName, code2 := guestBytes(m.Memory(), uint32(p[5]), uint32(p[6]))
	oldTrailing, newTrailing := strings.HasSuffix(oldName, "/"), strings.HasSuffix(newName, "/")
	od, oldName, c1 := e.resolve(uint32(p[0]), oldName)
	nd, newName, c2 := e.resolve(uint32(p[4]), newName)
	for _, c := range []uint64{code2, c1, c2} {
		if code == 0 {
			code = c
		}
	}
	if code == 0 {
		code = require(od, rightPathLinkSource)
	}
	if code == 0 {
		code = require(nd, rightPathLinkTarget)
	}
	oldFlags := uint16(p[1])
	if code == 0 && p[1]&^uint64(1) != 0 {
		code = wasiEInval
	}
	if code == 0 && oldTrailing {
		code = wasiENotdir
	}
	if code == 0 && newTrailing {
		code = wasiENoent
	}
	if code == 0 && od.mount != nd.mount {
		code = 75
	}
	if code == 0 {
		newParent, newLeaf, parentCode := openParent(nd, newName)
		code = parentCode
		if code == 0 {
			if oldFlags&1 != 0 {
				oldFile, openCode := openAt(od, oldName, unix.O_PATH, 0)
				code = openCode
				if code == 0 {
					code = errno(unix.Linkat(int(oldFile.Fd()), "", int(newParent.Fd()), newLeaf, unix.AT_EMPTY_PATH))
					_ = oldFile.Close()
				}
			} else {
				oldParent, oldLeaf, oldCode := openParent(od, oldName)
				code = oldCode
				if code == 0 {
					code = errno(unix.Linkat(int(oldParent.Fd()), oldLeaf, int(newParent.Fd()), newLeaf, 0))
					_ = oldParent.Close()
				}
			}
			_ = newParent.Close()
		}
	}
	r[0] = code
}

func (e *Extension) pathOpen(m wago.HostModule, p, r []uint64) {
	name, code := guestBytes(m.Memory(), uint32(p[2]), uint32(p[3]))
	trailingSlash := strings.HasSuffix(name, "/")
	d, name, pathCode := e.resolve(uint32(p[0]), name)
	if code == 0 {
		code = pathCode
	}
	if code == 0 {
		code = require(d, rightPathOpen)
	}
	oflags, rights, inheriting, fdflags := uint16(p[4]), p[5], p[6], uint16(p[7])
	if code == 0 && (p[1]&^uint64(1) != 0 || p[4]&^uint64(15) != 0 || p[7]&^uint64(31) != 0) {
		code = wasiEInval
	}
	if code == 0 && (rights&^d.inheriting != 0 || inheriting&^d.inheriting != 0) {
		code = wasiENotcapable
	}
	if code == 0 && oflags&1 != 0 {
		code = require(d, rightPathCreateFile)
	}
	if code == 0 && oflags&8 != 0 {
		code = require(d, rightPathFilestatSetSize)
	}
	flags := 0
	read, write := rights&rightFDRead != 0, rights&rightFDWrite != 0
	if read && write {
		flags = os.O_RDWR
	} else if write {
		flags = os.O_WRONLY
	} else {
		flags = os.O_RDONLY
	}
	if oflags&1 != 0 {
		flags |= os.O_CREATE
	}
	if oflags&4 != 0 {
		flags |= os.O_EXCL
	}
	if oflags&8 != 0 {
		flags |= os.O_TRUNC
	}
	if fdflags&1 != 0 {
		flags |= os.O_APPEND
	}
	if oflags&2 != 0 || trailingSlash {
		flags |= unix.O_DIRECTORY
	}
	if uint16(p[1])&1 == 0 {
		flags |= unix.O_NOFOLLOW
	}
	var f *os.File
	if code == 0 {
		f, code = openAt(d, name, flags, 0o666)
	}
	openedDir := false
	if code == 0 {
		st, err := f.Stat()
		if err != nil {
			code = errno(err)
		}
		if code == 0 && oflags&2 != 0 && !st.IsDir() {
			code = wasiENotdir
		}
		if code == 0 && st.IsDir() && write {
			code = wasiEIsdir
		}
		if code == 0 && st.IsDir() {
			rights &= directoryRights
			openedDir = true
		}
	}
	if code != 0 {
		if f != nil {
			_ = f.Close()
		}
	} else {
		entry := &fdEntry{file: f, mount: d.mount, flags: fdflags, rights: rights, inheriting: inheriting}
		if openedDir {
			entry.dirCookies = map[uint64]bool{0: true}
		}
		fd, allocCode := e.alloc(entry)
		if allocCode != 0 {
			_ = f.Close()
			code = allocCode
		} else if !putLe32(m.Memory(), uint32(p[8]), fd) {
			_ = f.Close()
			delete(e.fs.fds, fd)
			code = wasiEFault
		}
	}
	r[0] = code
}

func (e *Extension) pathReadlink(m wago.HostModule, p, r []uint64) {
	name, code := guestBytes(m.Memory(), uint32(p[1]), uint32(p[2]))
	d, name, pathCode := e.resolve(uint32(p[0]), name)
	if code == 0 {
		code = pathCode
	}
	if code == 0 {
		code = require(d, rightPathReadlink)
	}
	var target string
	if code == 0 {
		parent, leaf, parentCode := openParent(d, name)
		code = parentCode
		if code == 0 {
			buf := make([]byte, 4096)
			n, err := unix.Readlinkat(int(parent.Fd()), leaf, buf)
			_ = parent.Close()
			if err != nil {
				code = errno(err)
			} else if n == len(buf) {
				code = wasiENametoolong
			} else {
				target = string(buf[:n])
			}
		}
	}
	buf, n := uint32(p[3]), uint32(p[4])
	if code == 0 && uint64(buf)+uint64(n) > uint64(len(m.Memory())) {
		code = wasiEFault
	}
	if code == 0 {
		used := len(target)
		if used > int(n) {
			used = int(n)
		}
		copy(m.Memory()[buf:buf+uint32(used)], target[:used])
		if !putLe32(m.Memory(), uint32(p[5]), uint32(used)) {
			code = wasiEFault
		}
	}
	r[0] = code
}

func (e *Extension) pathRemoveDirectory(m wago.HostModule, p, r []uint64) {
	e.pathUnary(m, p, r, rightPathRemoveDirectory, func(fd int, name string) error {
		return unix.Unlinkat(fd, name, unix.AT_REMOVEDIR)
	})
}

func (e *Extension) pathUnlinkFile(m wago.HostModule, p, r []uint64) {
	name, code := guestBytes(m.Memory(), uint32(p[1]), uint32(p[2]))
	if code == 0 && strings.HasSuffix(name, "/") {
		d, clean, pathCode := e.resolve(uint32(p[0]), name)
		if pathCode != 0 {
			r[0] = pathCode
			return
		}
		f, openCode := openAt(d, clean, unix.O_PATH|unix.O_NOFOLLOW, 0)
		if openCode != 0 {
			r[0] = openCode
			return
		}
		st, err := f.Stat()
		_ = f.Close()
		if err == nil && st.IsDir() {
			r[0] = wasiEIsdir
		} else {
			r[0] = wasiENotdir
		}
		return
	}
	e.pathUnary(m, p, r, rightPathUnlinkFile, func(fd int, name string) error {
		return unix.Unlinkat(fd, name, 0)
	})
}

func (e *Extension) pathRename(m wago.HostModule, p, r []uint64) {
	oldName, code := guestBytes(m.Memory(), uint32(p[1]), uint32(p[2]))
	newName, code2 := guestBytes(m.Memory(), uint32(p[4]), uint32(p[5]))
	od, oldName, c1 := e.resolve(uint32(p[0]), oldName)
	nd, newName, c2 := e.resolve(uint32(p[3]), newName)
	for _, c := range []uint64{code2, c1, c2} {
		if code == 0 {
			code = c
		}
	}
	if code == 0 {
		code = require(od, rightPathRenameSource)
	}
	if code == 0 {
		code = require(nd, rightPathRenameTarget)
	}
	if code == 0 && od.mount != nd.mount {
		code = 75
	}
	if code == 0 {
		oldParent, oldLeaf, oldCode := openParent(od, oldName)
		code = oldCode
		if code == 0 {
			newParent, newLeaf, newCode := openParent(nd, newName)
			code = newCode
			if code == 0 {
				code = errno(unix.Renameat(int(oldParent.Fd()), oldLeaf, int(newParent.Fd()), newLeaf))
				_ = newParent.Close()
			}
			_ = oldParent.Close()
		}
	}
	r[0] = code
}

func (e *Extension) pathSymlink(m wago.HostModule, p, r []uint64) {
	target, code := guestBytes(m.Memory(), uint32(p[0]), uint32(p[1]))
	name, code2 := guestBytes(m.Memory(), uint32(p[3]), uint32(p[4]))
	trailingSlash := strings.HasSuffix(name, "/")
	d, name, pathCode := e.resolve(uint32(p[2]), name)
	for _, c := range []uint64{code2, pathCode} {
		if code == 0 {
			code = c
		}
	}
	if code == 0 {
		code = require(d, rightPathSymlink)
	}
	if code == 0 && strings.HasPrefix(target, "/") {
		code = wasiENotcapable
	}
	if code == 0 && trailingSlash {
		code = wasiENoent
	}
	if code == 0 {
		parent, leaf, parentCode := openParent(d, name)
		code = parentCode
		if code == 0 {
			code = errno(unix.Symlinkat(target, int(parent.Fd()), leaf))
			_ = parent.Close()
		}
	}
	r[0] = code
}

func (e *Extension) schedYield(_ wago.HostModule, _, r []uint64) { r[0] = wasiOK }
func (e *Extension) procRaise(_ wago.HostModule, _, r []uint64)  { r[0] = wasiENotsup }

func (e *Extension) sockAccept(_ wago.HostModule, _, r []uint64)   { r[0] = wasiENotsup }
func (e *Extension) sockRecv(_ wago.HostModule, p, r []uint64)     { e.unsupportedSocket(p, r) }
func (e *Extension) sockSend(_ wago.HostModule, p, r []uint64)     { e.unsupportedSocket(p, r) }
func (e *Extension) sockShutdown(_ wago.HostModule, p, r []uint64) { e.unsupportedSocket(p, r) }
func (e *Extension) unsupportedSocket(p, r []uint64) {
	if _, code := e.entry(uint32(p[0])); code != 0 {
		r[0] = code
	} else {
		r[0] = wasiENotsock
	}
}
