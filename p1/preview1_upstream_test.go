//go:build linux && amd64 && !tinygo

package p1_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"

	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/p1"
	"golang.org/x/sys/unix"
)

const (
	wasmI32 = byte(0x7f)
	wasmI64 = byte(0x7e)
)

type preview1Func struct {
	name   string
	params []byte
}

type preview1Harness struct {
	in *wago.Instance
}

func newPreview1Harness(t *testing.T, cfg p1.Config, funcs ...preview1Func) *preview1Harness {
	t.Helper()
	c, err := wago.Compile(nil, preview1CallModule(funcs))
	if err != nil {
		t.Fatalf("compile syscall harness: %v", err)
	}
	in, err := wago.Instantiate(c, wago.InstantiateOptions{Imports: p1.Imports(cfg)})
	if err != nil {
		t.Fatalf("instantiate syscall harness: %v", err)
	}
	t.Cleanup(func() { _ = in.Close() })
	return &preview1Harness{in: in}
}

func (h *preview1Harness) call(t *testing.T, name string, params ...uint64) uint32 {
	t.Helper()
	results, err := h.in.Invoke(name, params...)
	if err != nil {
		t.Fatalf("invoke %s: %v", name, err)
	}
	if len(results) != 1 {
		t.Fatalf("invoke %s returned %d results", name, len(results))
	}
	return uint32(results[0])
}

func (h *preview1Harness) memory() []byte { return h.in.Memory().Bytes() }

// preview1CallModule builds a tiny wasm module with one exported forwarding
// function per WASI import. Tests can call the real public p1.Imports boundary
// and inspect guest memory without checking internal implementation details.
func preview1CallModule(funcs []preview1Func) []byte {
	module := []byte{'\x00', 'a', 's', 'm', 1, 0, 0, 0}
	var types []byte
	types = appendULEB(types, uint32(len(funcs)))
	for _, fn := range funcs {
		types = append(types, 0x60)
		types = appendULEB(types, uint32(len(fn.params)))
		types = append(types, fn.params...)
		types = append(types, 1, wasmI32)
	}
	module = appendSection(module, 1, types)

	var imports []byte
	imports = appendULEB(imports, uint32(len(funcs)))
	for i, fn := range funcs {
		imports = appendName(imports, p1.Module)
		imports = appendName(imports, fn.name)
		imports = append(imports, 0)
		imports = appendULEB(imports, uint32(i))
	}
	module = appendSection(module, 2, imports)

	var functionSection []byte
	functionSection = appendULEB(functionSection, uint32(len(funcs)))
	for i := range funcs {
		functionSection = appendULEB(functionSection, uint32(i))
	}
	module = appendSection(module, 3, functionSection)
	module = appendSection(module, 5, []byte{1, 0, 1}) // one 64KiB memory

	var exports []byte
	exports = appendULEB(exports, uint32(len(funcs)+1))
	for i, fn := range funcs {
		exports = appendName(exports, fn.name)
		exports = append(exports, 0)
		exports = appendULEB(exports, uint32(len(funcs)+i))
	}
	exports = appendName(exports, "memory")
	exports = append(exports, 2, 0)
	module = appendSection(module, 7, exports)

	var code []byte
	code = appendULEB(code, uint32(len(funcs)))
	for i, fn := range funcs {
		body := []byte{0} // no locals
		for param := range fn.params {
			body = append(body, 0x20)
			body = appendULEB(body, uint32(param))
		}
		body = append(body, 0x10)
		body = appendULEB(body, uint32(i))
		body = append(body, 0x0b)
		code = appendULEB(code, uint32(len(body)))
		code = append(code, body...)
	}
	return appendSection(module, 10, code)
}

func appendSection(dst []byte, id byte, payload []byte) []byte {
	dst = append(dst, id)
	dst = appendULEB(dst, uint32(len(payload)))
	return append(dst, payload...)
}

func appendName(dst []byte, value string) []byte {
	dst = appendULEB(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendULEB(dst []byte, value uint32) []byte {
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			b |= 0x80
		}
		dst = append(dst, b)
		if value == 0 {
			return dst
		}
	}
}

// Ported from Wazero's Test_fdReaddir_Errors: dircookie is opaque and a
// non-zero cookie is invalid until the host returned it in a prior dirent.
func TestPreview1WazeroFdReaddirRejectsUnissuedCookie(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(root+"/dir", 0o755); err != nil {
		t.Fatal(err)
	}
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
		preview1Func{"fd_readdir", []byte{wasmI32, wasmI32, wasmI32, wasmI64, wasmI32}},
	)
	copy(h.memory()[32:], "dir")
	const rightReadDir = uint64(1 << 14)
	if errno := h.call(t, "path_open", 3, 0, 32, 3, 2, rightReadDir, 0, 0, 16); errno != 0 {
		t.Fatalf("path_open errno = %d", errno)
	}
	fd := binary.LittleEndian.Uint32(h.memory()[16:])
	if errno := h.call(t, "fd_readdir", uint64(fd), 128, 256, 1, 24); errno != 44 {
		t.Fatalf("fd_readdir with unissued cookie errno = %d, want ENOENT(44)", errno)
	}
}

const (
	errnoSuccess    = uint32(0)
	errnoBadf       = uint32(8)
	errnoFault      = uint32(21)
	errnoInval      = uint32(28)
	errnoIO         = uint32(29)
	errnoLoop       = uint32(32)
	errnoMfile      = uint32(33)
	errnoNoent      = uint32(44)
	errnoNotsock    = uint32(57)
	errnoNotcapable = uint32(76)

	rightFDRead             = uint64(1 << 1)
	rightFDSeek             = uint64(1 << 2)
	rightFDTell             = uint64(1 << 5)
	rightFDWrite            = uint64(1 << 6)
	rightFDStatSetFlags     = uint64(1 << 3)
	rightFDReadDir          = uint64(1 << 14)
	rightFDFilestatGet      = uint64(1 << 21)
	rightFDFilestatSetSize  = uint64(1 << 22)
	rightFDFilestatSetTimes = uint64(1 << 23)
	rightPollReadWrite      = uint64(1 << 27)
)

func requireErrno(t *testing.T, want, got uint32) {
	t.Helper()
	if got != want {
		t.Fatalf("errno = %d, want %d", got, want)
	}
}

// Ported from Wazero's args_get/environ_get and sizes tests, including the
// exact pointer-vector and NUL-terminated string layout mandated by Preview 1.
func TestPreview1WazeroArgsAndEnvironmentLayout(t *testing.T) {
	h := newPreview1Harness(t, p1.Config{Args: []string{"a", "bc"}, Env: []string{"A=b", "B=cd"}},
		preview1Func{"args_sizes_get", []byte{wasmI32, wasmI32}},
		preview1Func{"args_get", []byte{wasmI32, wasmI32}},
		preview1Func{"environ_sizes_get", []byte{wasmI32, wasmI32}},
		preview1Func{"environ_get", []byte{wasmI32, wasmI32}},
	)
	requireErrno(t, errnoSuccess, h.call(t, "args_sizes_get", 16, 20))
	if count, size := binary.LittleEndian.Uint32(h.memory()[16:]), binary.LittleEndian.Uint32(h.memory()[20:]); count != 2 || size != 5 {
		t.Fatalf("args sizes = (%d, %d), want (2, 5)", count, size)
	}
	requireErrno(t, errnoSuccess, h.call(t, "args_get", 32, 64))
	if p0, p1 := binary.LittleEndian.Uint32(h.memory()[32:]), binary.LittleEndian.Uint32(h.memory()[36:]); p0 != 64 || p1 != 66 {
		t.Fatalf("argv pointers = (%d, %d), want (64, 66)", p0, p1)
	}
	if got := h.memory()[64:69]; !bytes.Equal(got, []byte{'a', 0, 'b', 'c', 0}) {
		t.Fatalf("argv buffer = %v", got)
	}

	requireErrno(t, errnoSuccess, h.call(t, "environ_sizes_get", 24, 28))
	if count, size := binary.LittleEndian.Uint32(h.memory()[24:]), binary.LittleEndian.Uint32(h.memory()[28:]); count != 2 || size != 9 {
		t.Fatalf("environment sizes = (%d, %d), want (2, 9)", count, size)
	}
	requireErrno(t, errnoSuccess, h.call(t, "environ_get", 96, 128))
	if p0, p1 := binary.LittleEndian.Uint32(h.memory()[96:]), binary.LittleEndian.Uint32(h.memory()[100:]); p0 != 128 || p1 != 132 {
		t.Fatalf("environment pointers = (%d, %d), want (128, 132)", p0, p1)
	}
	if got := h.memory()[128:137]; !bytes.Equal(got, []byte("A=b\x00B=cd\x00")) {
		t.Fatalf("environment buffer = %v", got)
	}
	requireErrno(t, errnoFault, h.call(t, "args_get", 65536, 0))
	requireErrno(t, errnoFault, h.call(t, "environ_get", 0, 65536))
}

// Ported from Wazero's args, clock, poll, random and fdstat error tables.
func TestPreview1WazeroMemoryAndArgumentErrors(t *testing.T) {
	h := newPreview1Harness(t, p1.Config{Args: []string{"wasi-test"}},
		preview1Func{"args_sizes_get", []byte{wasmI32, wasmI32}},
		preview1Func{"clock_time_get", []byte{wasmI32, wasmI64, wasmI32}},
		preview1Func{"fd_fdstat_get", []byte{wasmI32, wasmI32}},
		preview1Func{"poll_oneoff", []byte{wasmI32, wasmI32, wasmI32, wasmI32}},
		preview1Func{"random_get", []byte{wasmI32, wasmI32}},
	)

	tests := []struct {
		name string
		call string
		args []uint64
		want uint32
	}{
		{"args count pointer out of memory", "args_sizes_get", []uint64{65536, 0}, errnoFault},
		{"unsupported clock id", "clock_time_get", []uint64{4, 0, 0}, errnoInval},
		{"clock result out of memory", "clock_time_get", []uint64{0, 0, 65536}, errnoFault},
		{"fdstat result out of memory", "fd_fdstat_get", []uint64{0, 65536}, errnoFault},
		{"poll subscriptions out of memory", "poll_oneoff", []uint64{65536, 128, 1, 512}, errnoFault},
		{"poll events out of memory", "poll_oneoff", []uint64{0, 65536, 1, 512}, errnoFault},
		{"poll result out of memory", "poll_oneoff", []uint64{0, 128, 1, 65536}, errnoFault},
		{"poll rejects zero subscriptions", "poll_oneoff", []uint64{0, 128, 0, 512}, errnoInval},
		{"random buffer out of memory", "random_get", []uint64{65536, 1}, errnoFault},
		{"random length wraps memory", "random_get", []uint64{0, 65537}, errnoFault},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireErrno(t, test.want, h.call(t, test.call, test.args...))
		})
	}
}

// Ported from Wazero's Test_randomGet_SourceError.
func TestPreview1WazeroRandomSourceErrorsAreIO(t *testing.T) {
	tests := []struct {
		name string
		rand io.Reader
	}{
		{"error", errorReader{err: errors.New("random source failed")}},
		{"short read", bytes.NewReader([]byte{1})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newPreview1Harness(t, p1.Config{Rand: test.rand}, preview1Func{"random_get", []byte{wasmI32, wasmI32}})
			requireErrno(t, errnoIO, h.call(t, "random_get", 1, 5))
		})
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

// Ported from Wazero's fd_prestat tests and Wasmtime's path_open_preopen test.
func TestPreview1UpstreamPreopenMetadataAndClose(t *testing.T) {
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": t.TempDir()}},
		preview1Func{"fd_prestat_get", []byte{wasmI32, wasmI32}},
		preview1Func{"fd_prestat_dir_name", []byte{wasmI32, wasmI32, wasmI32}},
		preview1Func{"fd_fdstat_get", []byte{wasmI32, wasmI32}},
		preview1Func{"fd_close", []byte{wasmI32}},
	)
	requireErrno(t, errnoSuccess, h.call(t, "fd_prestat_get", 3, 16))
	if got := binary.LittleEndian.Uint32(h.memory()[20:]); got != 1 {
		t.Fatalf("preopen name length = %d, want 1", got)
	}
	requireErrno(t, errnoSuccess, h.call(t, "fd_prestat_dir_name", 3, 32, 1))
	if got := h.memory()[32]; got != '/' {
		t.Fatalf("preopen name = %q, want /", got)
	}
	requireErrno(t, errnoBadf, h.call(t, "fd_prestat_get", 42, 16))
	requireErrno(t, errnoFault, h.call(t, "fd_prestat_get", 3, 65536))
	requireErrno(t, errnoSuccess, h.call(t, "fd_close", 3))
	requireErrno(t, errnoBadf, h.call(t, "fd_fdstat_get", 3, 64))
}

// Ported from Wazero's descriptor I/O tests and the Wasmtime P1 file programs.
func TestPreview1UpstreamDescriptorRightsAndIO(t *testing.T) {
	root := t.TempDir()
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
		preview1Func{"fd_write", []byte{wasmI32, wasmI32, wasmI32, wasmI32}},
		preview1Func{"fd_seek", []byte{wasmI32, wasmI64, wasmI32, wasmI32}},
		preview1Func{"fd_read", []byte{wasmI32, wasmI32, wasmI32, wasmI32}},
		preview1Func{"fd_fdstat_set_rights", []byte{wasmI32, wasmI64, wasmI64}},
		preview1Func{"fd_fdstat_get", []byte{wasmI32, wasmI32}},
		preview1Func{"fd_close", []byte{wasmI32}},
	)
	copy(h.memory()[32:], "file")
	originalRights := rightFDRead | rightFDSeek | rightFDTell | rightFDWrite
	requireErrno(t, errnoSuccess, h.call(t, "path_open", 3, 0, 32, 4, 1, originalRights, 0, 0, 16))
	fd := uint64(binary.LittleEndian.Uint32(h.memory()[16:]))
	copy(h.memory()[128:], "hello")
	binary.LittleEndian.PutUint32(h.memory()[96:], 128)
	binary.LittleEndian.PutUint32(h.memory()[100:], 5)
	requireErrno(t, errnoSuccess, h.call(t, "fd_write", fd, 96, 1, 24))
	requireErrno(t, errnoSuccess, h.call(t, "fd_seek", fd, 0, 0, 40))
	binary.LittleEndian.PutUint32(h.memory()[104:], 160)
	binary.LittleEndian.PutUint32(h.memory()[108:], 5)
	requireErrno(t, errnoSuccess, h.call(t, "fd_read", fd, 104, 1, 28))
	if got := string(h.memory()[160:165]); got != "hello" {
		t.Fatalf("fd_read = %q, want hello", got)
	}

	reduced := rightFDRead | rightFDSeek | rightFDTell
	requireErrno(t, errnoSuccess, h.call(t, "fd_fdstat_set_rights", fd, reduced, 0))
	requireErrno(t, errnoNotcapable, h.call(t, "fd_write", fd, 96, 1, 24))
	requireErrno(t, errnoNotcapable, h.call(t, "fd_fdstat_set_rights", fd, originalRights, 0))
	requireErrno(t, errnoSuccess, h.call(t, "fd_fdstat_get", fd, 192))
	if got := binary.LittleEndian.Uint64(h.memory()[200:]); got != reduced {
		t.Fatalf("reduced rights = %#x, want %#x", got, reduced)
	}
	requireErrno(t, errnoSuccess, h.call(t, "fd_close", fd))
	requireErrno(t, errnoBadf, h.call(t, "fd_close", fd))
}

// Ported from Wazero's fd_pread/fd_pwrite offset and filestat tests. Positioned
// I/O must scatter/gather correctly without changing the descriptor cursor.
func TestPreview1WazeroPositionedIOAndFilestat(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/file", []byte("wazero"), 0o600); err != nil {
		t.Fatal(err)
	}
	rights := rightFDRead | rightFDWrite | rightFDSeek | rightFDTell |
		rightFDFilestatGet | rightFDFilestatSetSize | rightFDFilestatSetTimes | rightFDStatSetFlags
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
		preview1Func{"fd_pread", []byte{wasmI32, wasmI32, wasmI32, wasmI64, wasmI32}},
		preview1Func{"fd_pwrite", []byte{wasmI32, wasmI32, wasmI32, wasmI64, wasmI32}},
		preview1Func{"fd_read", []byte{wasmI32, wasmI32, wasmI32, wasmI32}},
		preview1Func{"fd_tell", []byte{wasmI32, wasmI32}},
		preview1Func{"fd_filestat_get", []byte{wasmI32, wasmI32}},
		preview1Func{"fd_filestat_set_size", []byte{wasmI32, wasmI64}},
		preview1Func{"fd_filestat_set_times", []byte{wasmI32, wasmI64, wasmI64, wasmI32}},
	)
	copy(h.memory()[32:], "file")
	requireErrno(t, errnoSuccess, h.call(t, "path_open", 3, 0, 32, 4, 0, rights, 0, 0, 16))
	fd := uint64(binary.LittleEndian.Uint32(h.memory()[16:]))

	// Two iovecs gather "zero" from file offset 2 into non-contiguous memory.
	binary.LittleEndian.PutUint32(h.memory()[64:], 128)
	binary.LittleEndian.PutUint32(h.memory()[68:], 2)
	binary.LittleEndian.PutUint32(h.memory()[72:], 132)
	binary.LittleEndian.PutUint32(h.memory()[76:], 2)
	requireErrno(t, errnoSuccess, h.call(t, "fd_pread", fd, 64, 2, 2, 24))
	if got := string(append(append([]byte(nil), h.memory()[128:130]...), h.memory()[132:134]...)); got != "zero" {
		t.Fatalf("fd_pread = %q, want zero", got)
	}
	if got := binary.LittleEndian.Uint32(h.memory()[24:]); got != 4 {
		t.Fatalf("fd_pread nread = %d, want 4", got)
	}

	copy(h.memory()[160:], "XY")
	binary.LittleEndian.PutUint32(h.memory()[80:], 160)
	binary.LittleEndian.PutUint32(h.memory()[84:], 2)
	requireErrno(t, errnoSuccess, h.call(t, "fd_pwrite", fd, 80, 1, 1, 28))
	requireErrno(t, errnoSuccess, h.call(t, "fd_tell", fd, 40))
	if got := binary.LittleEndian.Uint64(h.memory()[40:]); got != 0 {
		t.Fatalf("descriptor cursor after positioned I/O = %d, want 0", got)
	}

	// A normal read still begins at offset zero and observes the positioned write.
	binary.LittleEndian.PutUint32(h.memory()[88:], 192)
	binary.LittleEndian.PutUint32(h.memory()[92:], 6)
	requireErrno(t, errnoSuccess, h.call(t, "fd_read", fd, 88, 1, 30))
	if got := string(h.memory()[192:198]); got != "wXYero" {
		t.Fatalf("file after fd_pwrite = %q, want wXYero", got)
	}

	requireErrno(t, errnoSuccess, h.call(t, "fd_filestat_set_size", fd, 3))
	requireErrno(t, errnoSuccess, h.call(t, "fd_filestat_get", fd, 224))
	if got := binary.LittleEndian.Uint64(h.memory()[256:]); got != 3 {
		t.Fatalf("filestat size = %d, want 3", got)
	}
	const unixSecond = uint64(1_600_000_000_000_000_000)
	requireErrno(t, errnoSuccess, h.call(t, "fd_filestat_set_times", fd, unixSecond, unixSecond, 5))
	st, err := os.Stat(root + "/file")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.ModTime().Unix(); got != int64(unixSecond/1_000_000_000) {
		t.Fatalf("mtime = %d, want %d", got, unixSecond/1_000_000_000)
	}

	requireErrno(t, errnoFault, h.call(t, "fd_pread", fd, 65532, 1, 0, 24))
	requireErrno(t, errnoBadf, h.call(t, "fd_pwrite", 42, 80, 1, 0, 28))
}

// Ported from Wazero's path_open error table and Wasmtime's interesting-paths,
// nofollow-errors and dangling-symlink programs.
func TestPreview1UpstreamPathConfinementAndSymlinks(t *testing.T) {
	base := t.TempDir()
	root := base + "/root"
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/inside", []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+"/outside", []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside", root+"/inside-link"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", root+"/escape"); err != nil {
		t.Fatal(err)
	}
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
		preview1Func{"fd_close", []byte{wasmI32}},
	)
	for offset, path := range map[uint32]string{32: "/outside", 64: "../outside", 96: "escape", 128: "inside-link"} {
		copy(h.memory()[offset:], path)
	}
	requireErrno(t, errnoNotcapable, h.call(t, "path_open", 3, 0, 32, 8, 0, rightFDRead, 0, 0, 16))
	requireErrno(t, errnoNotcapable, h.call(t, "path_open", 3, 0, 64, 10, 0, rightFDRead, 0, 0, 16))
	requireErrno(t, errnoNotcapable, h.call(t, "path_open", 3, 1, 96, 6, 0, rightFDRead, 0, 0, 16))
	requireErrno(t, errnoLoop, h.call(t, "path_open", 3, 0, 128, 11, 0, rightFDRead, 0, 0, 16))
	requireErrno(t, errnoSuccess, h.call(t, "path_open", 3, 1, 128, 11, 0, rightFDRead, 0, 0, 16))
	requireErrno(t, errnoSuccess, h.call(t, "fd_close", uint64(binary.LittleEndian.Uint32(h.memory()[16:]))))
	requireErrno(t, errnoInval, h.call(t, "path_open", 3, 0, 128, 11, 16, rightFDRead, 0, 0, 16))
	requireErrno(t, errnoInval, h.call(t, "path_open", 3, 0, 128, 11, 0, rightFDRead, 0, 32, 16))
}

// Ported from Wazero's path filestat, hard-link, rename, unlink, and directory
// mutation suites. Every operation remains relative to the preopen capability.
func TestPreview1WazeroPathMutations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/source", []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"path_filestat_get", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32}},
		preview1Func{"path_link", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI32}},
		preview1Func{"path_rename", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI32}},
		preview1Func{"path_unlink_file", []byte{wasmI32, wasmI32, wasmI32}},
		preview1Func{"path_create_directory", []byte{wasmI32, wasmI32, wasmI32}},
		preview1Func{"path_remove_directory", []byte{wasmI32, wasmI32, wasmI32}},
	)
	for offset, name := range map[uint32]string{
		32: "source", 64: "linked", 96: "renamed", 128: "directory",
	} {
		copy(h.memory()[offset:], name)
	}

	requireErrno(t, errnoSuccess, h.call(t, "path_filestat_get", 3, 0, 32, 6, 192))
	if got := h.memory()[208]; got != 4 { // __wasi_filetype_t::regular_file
		t.Fatalf("source filetype = %d, want regular file (4)", got)
	}
	if got := binary.LittleEndian.Uint64(h.memory()[224:]); got != 7 {
		t.Fatalf("source size = %d, want 7", got)
	}

	requireErrno(t, errnoSuccess, h.call(t, "path_link", 3, 0, 32, 6, 3, 64, 6))
	if a, err := os.ReadFile(root + "/linked"); err != nil || string(a) != "payload" {
		t.Fatalf("hard link contents = %q, %v", a, err)
	}
	requireErrno(t, errnoSuccess, h.call(t, "path_rename", 3, 64, 6, 3, 96, 7))
	if _, err := os.Stat(root + "/linked"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old hard-link name remains after rename: %v", err)
	}
	if a, err := os.ReadFile(root + "/renamed"); err != nil || string(a) != "payload" {
		t.Fatalf("renamed contents = %q, %v", a, err)
	}
	requireErrno(t, errnoSuccess, h.call(t, "path_unlink_file", 3, 96, 7))
	if _, err := os.Stat(root + "/renamed"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unlinked path remains: %v", err)
	}

	requireErrno(t, errnoSuccess, h.call(t, "path_create_directory", 3, 128, 9))
	if st, err := os.Stat(root + "/directory"); err != nil || !st.IsDir() {
		t.Fatalf("created directory = %v, %v", st, err)
	}
	requireErrno(t, errnoSuccess, h.call(t, "path_remove_directory", 3, 128, 9))
	requireErrno(t, errnoNoent, h.call(t, "path_filestat_get", 3, 0, 128, 9, 192))
	requireErrno(t, errnoFault, h.call(t, "path_unlink_file", 3, 65536, 1))
}

// Ported from Wazero's readdir rewind/dot-inode tests.
func TestPreview1WazeroFdReaddirIssuedCookiesAndDotInode(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(root+"/dir", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/dir/file", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
		preview1Func{"fd_readdir", []byte{wasmI32, wasmI32, wasmI32, wasmI64, wasmI32}},
	)
	copy(h.memory()[32:], "dir")
	requireErrno(t, errnoSuccess, h.call(t, "path_open", 3, 0, 32, 3, 2, rightFDReadDir, 0, 0, 16))
	fd := uint64(binary.LittleEndian.Uint32(h.memory()[16:]))
	requireErrno(t, errnoSuccess, h.call(t, "fd_readdir", fd, 128, 25, 0, 24))
	if ino := binary.LittleEndian.Uint64(h.memory()[136:]); ino == 0 {
		t.Fatal("dot entry inode is zero")
	}
	cookie := binary.LittleEndian.Uint64(h.memory()[128:])
	if cookie == 0 {
		t.Fatal("fd_readdir returned a zero continuation cookie")
	}
	requireErrno(t, errnoSuccess, h.call(t, "fd_readdir", fd, 256, 256, cookie, 28))
	if used := binary.LittleEndian.Uint32(h.memory()[28:]); used == 0 {
		t.Fatal("fd_readdir continuation returned no entries")
	}
}

// Ported from Wasmtime's p1_poll_oneoff_files guest test.
func TestPreview1WasmtimePollOneoffFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/file", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
		preview1Func{"poll_oneoff", []byte{wasmI32, wasmI32, wasmI32, wasmI32}},
	)
	copy(h.memory()[32:], "file")
	rights := rightFDRead | rightFDWrite | rightPollReadWrite
	requireErrno(t, errnoSuccess, h.call(t, "path_open", 3, 0, 32, 4, 0, rights, 0, 0, 16))
	fd := binary.LittleEndian.Uint32(h.memory()[16:])
	mem := h.memory()
	binary.LittleEndian.PutUint64(mem[0:], 0x111)
	mem[8] = 1
	binary.LittleEndian.PutUint32(mem[16:], fd)
	binary.LittleEndian.PutUint64(mem[48:], 0x222)
	mem[56] = 2
	binary.LittleEndian.PutUint32(mem[64:], fd)
	requireErrno(t, errnoSuccess, h.call(t, "poll_oneoff", 0, 128, 2, 112))
	if got := binary.LittleEndian.Uint32(mem[112:]); got != 2 {
		t.Fatalf("nevents = %d, want 2", got)
	}
	if mem[138] != 1 || mem[170] != 2 {
		t.Fatalf("event types = %d, %d; want read, write", mem[138], mem[170])
	}
}

// Ported from Wasmtime's p1_path_open_lots stress guest.
func TestPreview1WasmtimePathOpenLots(t *testing.T) {
	root := t.TempDir()
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
		preview1Func{"fd_close", []byte{wasmI32}},
	)
	copy(h.memory()[32:], "many")
	fds := make([]uint32, 256)
	for i := range fds {
		requireErrno(t, errnoSuccess, h.call(t, "path_open", 3, 0, 32, 4, 1, rightFDRead, 0, 0, 16))
		fds[i] = binary.LittleEndian.Uint32(h.memory()[16:])
	}
	for _, fd := range fds {
		requireErrno(t, errnoSuccess, h.call(t, "fd_close", uint64(fd)))
	}
}

// Ported from Wazero's socket error tables.
func TestPreview1WazeroSocketDescriptorErrors(t *testing.T) {
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": t.TempDir()}},
		preview1Func{"sock_shutdown", []byte{wasmI32, wasmI32}},
	)
	requireErrno(t, errnoBadf, h.call(t, "sock_shutdown", 42, 3))
	requireErrno(t, errnoNotsock, h.call(t, "sock_shutdown", 3, 3))
}

func TestPreview1RightsAttenuationCannotBeReEscalated(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(root+"/child", 0o755); err != nil {
		t.Fatal(err)
	}
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"fd_fdstat_get", []byte{wasmI32, wasmI32}},
		preview1Func{"fd_fdstat_set_rights", []byte{wasmI32, wasmI64, wasmI64}},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
	)
	requireErrno(t, errnoSuccess, h.call(t, "fd_fdstat_get", 3, 64))
	base := binary.LittleEndian.Uint64(h.memory()[72:])
	requireErrno(t, errnoSuccess, h.call(t, "fd_fdstat_set_rights", 3, base, rightFDReadDir))
	copy(h.memory()[32:], "child")
	requireErrno(t, errnoNotcapable, h.call(t, "path_open", 3, 0, 32, 5, 2, rightFDReadDir, rightFDRead, 0, 16))
}

func TestPreview1OpenFileQuota(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/file", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}, MaxOpenFiles: 4},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
	)
	copy(h.memory()[32:], "file")
	requireErrno(t, errnoMfile, h.call(t, "path_open", 3, 0, 32, 4, 0, rightFDRead, 0, 0, 16))
}

func TestPreview1SymlinkSwapCannotEscapePreopen(t *testing.T) {
	base := t.TempDir()
	root, outside := base+"/root", base+"/outside"
	for _, dir := range []string{root, outside, root + "/gate"} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(root+"/gate/victim", []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside+"/victim", []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", root+"/swap"); err != nil {
		t.Fatal(err)
	}

	h := newPreview1Harness(t, p1.Config{Preopens: map[string]string{"/": root}},
		preview1Func{"path_open", []byte{wasmI32, wasmI32, wasmI32, wasmI32, wasmI32, wasmI64, wasmI64, wasmI32, wasmI32}},
		preview1Func{"fd_close", []byte{wasmI32}},
	)
	copy(h.memory()[32:], "gate/victim")
	stop := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				if err := unix.Renameat2(unix.AT_FDCWD, root+"/gate", unix.AT_FDCWD, root+"/swap", unix.RENAME_EXCHANGE); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()
	for i := 0; i < 1000; i++ {
		errno := h.call(t, "path_open", 3, 1, 32, 11, 8, rightFDWrite, 0, 0, 16)
		if errno == errnoSuccess {
			requireErrno(t, errnoSuccess, h.call(t, "fd_close", uint64(binary.LittleEndian.Uint32(h.memory()[16:]))))
		} else if errno != errnoNotcapable && errno != errnoNoent {
			t.Fatalf("path_open during swap errno = %d", errno)
		}
	}
	close(stop)
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
	got, err := os.ReadFile(outside + "/victim")
	if err != nil || string(got) != "outside-secret" {
		t.Fatalf("outside capability was modified: %q, %v", got, err)
	}
}
