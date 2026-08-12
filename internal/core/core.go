// Package core is the shared implementation behind the versioned WASI plugins.
// The command and filesystem surface is shared across wasi_unstable
// (pre-preview1) and wasi_snapshot_preview1; only the wasm
// import module name differs, so both wrap this package with their own module
// string. It is internal: use github.com/wago-org/wasi/p1 or
// github.com/wago-org/wasi/unstable.
package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	wago "github.com/wago-org/wago"
)

// Guest capabilities are deliberately narrower than "WASI". A policy can
// allow stdout without filesystem mutation, argv without environment access,
// or clocks without randomness. Descriptor and path capabilities remain
// coarse enough to match Preview 1's descriptor-multiplexed ABI honestly.
const (
	CapFDRead          wago.Capability = "wasi.fd.read"
	CapFDWrite         wago.Capability = "wasi.fd.write"
	CapFDManage        wago.Capability = "wasi.fd.manage"
	CapPathRead        wago.Capability = "wasi.path.read"
	CapPathWrite       wago.Capability = "wasi.path.write"
	CapArgumentsRead   wago.Capability = "wasi.arguments.read"
	CapEnvironmentRead wago.Capability = "wasi.environment.read"
	CapClockRead       wago.Capability = "wasi.clock.read"
	CapRandomRead      wago.Capability = "wasi.random.read"
	CapProcessExit     wago.Capability = "wasi.process.exit"
	CapPoll            wago.Capability = "wasi.poll"
	CapSchedulerYield  wago.Capability = "wasi.scheduler.yield"
	CapUnsupported     wago.Capability = "wasi.unsupported"
)

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
	// MaxOpenFiles bounds the host descriptors owned by one guest instance,
	// including stdio and preopens. Zero uses the secure default of 1024.
	MaxOpenFiles uint32
	// MaxPollDuration rejects clock subscriptions longer than this duration so a
	// guest cannot pin a host call indefinitely. Zero uses one second.
	MaxPollDuration time.Duration
}

type pluginConfig struct {
	Stdin                 *string            `json:"stdin,omitempty"`
	Stdout                *string            `json:"stdout,omitempty"`
	Stderr                *string            `json:"stderr,omitempty"`
	Env                   *[]string          `json:"env,omitempty"`
	Preopens              *map[string]string `json:"preopens,omitempty"`
	MaxOpenFiles          *uint32            `json:"maxOpenFiles,omitempty"`
	MaxPollDurationMillis *int64             `json:"maxPollDurationMillis,omitempty"`
}

var configSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "stdin": {"type": "string", "enum": ["inherit", "eof"]},
    "stdout": {"type": "string", "enum": ["inherit", "discard"]},
    "stderr": {"type": "string", "enum": ["inherit", "discard"]},
    "env": {
      "type": "array",
      "maxItems": 4096,
      "items": {"type": "string", "minLength": 2, "maxLength": 32768, "pattern": "^[^=\\u0000]+=[^\\u0000]*$"}
    },
    "preopens": {
      "type": "object",
      "maxProperties": 64,
      "propertyNames": {"type": "string", "pattern": "^/(?:[^/\\u0000]+(?:/[^/\\u0000]+)*)?$", "maxLength": 4096},
      "additionalProperties": {"type": "string", "minLength": 1, "maxLength": 4096}
    },
    "maxOpenFiles": {"type": "integer", "minimum": 3, "maximum": 65536},
    "maxPollDurationMillis": {"type": "integer", "minimum": 1, "maximum": 60000}
  }
}`)

const maxConfigBytes = 256 << 10

// ConfigSchema returns a fresh copy of the strict JSON configuration schema
// embedded in every WASI provider definition.
func ConfigSchema() json.RawMessage {
	return append(json.RawMessage(nil), configSchema...)
}

// Provider builds one side-effect-free catalog entry. The caller supplies the
// immutable package-specific definition and exact Preview 1 import module.
func Provider(definition wago.PluginDefinition, module string) wago.PluginProvider {
	return wago.PluginProvider{
		Definition: definition,
		New: func() wago.Plugin {
			return &Plugin{module: module}
		},
		ValidateConfig: validatePluginConfig,
	}
}

// Plugin is a WASI provider bound to one exact Wasm import module. It has no
// package-global registration or process-global argv state.
type Plugin struct {
	module    string
	cfg       Config
	arguments *wago.GuestArgumentsAccess
	fs        *fsState
	guard     *fsGuard
}

// Register declares imports, guest capabilities, and lifecycle through exact
// vNext handles. Filesystem resources are opened only during Start.
func (e *Plugin) Register(reg *wago.Registrar) error {
	var cfg pluginConfig
	if err := reg.Config(&cfg); err != nil {
		return err
	}
	resolved, err := configFromPluginConfig(cfg)
	if err != nil {
		return err
	}
	e.cfg = resolved
	e.resetFS()

	e.arguments, err = reg.GuestArguments()
	if err != nil {
		return err
	}
	imports, err := reg.HostImports()
	if err != nil {
		return err
	}
	m, err := imports.Module(e.module)
	if err != nil {
		return err
	}
	e.guard.resolver, err = reg.HostCallers()
	if err != nil {
		return err
	}
	closed, err := reg.InstanceCloseObserver()
	if err != nil {
		return err
	}
	if err := closed.After(func(event wago.InstanceCloseEvent) { e.closeInstance(event.Instance) }); err != nil {
		return err
	}
	for _, capability := range guestCapabilities {
		if err := reg.GuestCapability(capability.cap, wago.CapabilityDocs(capability.docs)); err != nil {
			return err
		}
	}
	for _, b := range e.bindings() {
		m.Func(b.name, b.fn).Params(b.params...).Results(b.results...).Capability(b.cap).Docs(b.docs)
	}
	return reg.Lifecycle(wago.PluginLifecycle{Start: e.start, Stop: e.stop})
}

func (e *Plugin) start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	args, err := e.arguments.Args()
	if err != nil {
		return err
	}
	e.cfg.Args = args
	return e.initFS(true)
}

func (e *Plugin) stop(context.Context) error {
	e.closeAll()
	return nil
}

// Imports returns a raw low-level host bundle. This API intentionally remains
// available for embedders that do not use Runtime.LoadPlugins. Plugin policy,
// lifecycle cleanup, and runtime-scoped argv apply only to Provider.
func Imports(module string, cfg Config) wago.Imports {
	e := &Plugin{module: module, cfg: cloneConfig(cfg)}
	e.resetFS()
	_ = e.initFS(false) // preserve the raw API's historical best-effort preopens
	return e.Imports()
}

func (e *Plugin) Imports() wago.Imports {
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
	cap             wago.Capability
	docs            string
}

type guestCapability struct {
	cap  wago.Capability
	docs string
}

var guestCapabilities = []guestCapability{
	{CapFDRead, "read streams and granted file descriptors"},
	{CapFDWrite, "write streams and granted file descriptors"},
	{CapFDManage, "close, seek, inspect, and renumber descriptors"},
	{CapPathRead, "inspect paths below configured preopens"},
	{CapPathWrite, "mutate paths below configured preopens"},
	{CapArgumentsRead, "read runtime-scoped guest argv"},
	{CapEnvironmentRead, "read the configured guest environment"},
	{CapClockRead, "read host clocks"},
	{CapRandomRead, "read cryptographic host randomness"},
	{CapProcessExit, "terminate guest execution with a status code"},
	{CapPoll, "wait for descriptor and clock events"},
	{CapSchedulerYield, "yield guest execution"},
	{CapUnsupported, "call unsupported Preview 1 compatibility stubs"},
}

func (e *Plugin) bindings() []binding {
	i32 := []wago.ValType{wago.ValI32}
	i32x2 := []wago.ValType{wago.ValI32, wago.ValI32}
	i32x3 := []wago.ValType{wago.ValI32, wago.ValI32, wago.ValI32}
	i32x4 := []wago.ValType{wago.ValI32, wago.ValI32, wago.ValI32, wago.ValI32}
	i64 := wago.ValI64
	i32v := wago.ValI32

	bindings := []binding{
		{"fd_write", e.fdWrite, i32x4, i32, CapFDWrite, "write iovecs to a file descriptor (stdout/stderr)"},
		{"fd_read", e.fdRead, i32x4, i32, CapFDRead, "read into iovecs from a file descriptor (stdin)"},
		{"fd_close", e.fdClose, i32, i32, CapFDManage, "close a file descriptor (streams: no-op)"},
		{"fd_seek", e.fdSeek, []wago.ValType{i32v, i64, i32v, i32v}, i32, CapFDManage, "seek a file descriptor (streams: ESPIPE)"},
		{"fd_fdstat_get", e.fdFdstatGet, i32x2, i32, CapFDManage, "report fd stat (streams: character device)"},
		{"fd_prestat_get", e.fdPrestatGet, i32x2, i32, CapFDManage, "report a preopen (none: EBADF)"},
		{"fd_prestat_dir_name", e.fdPrestatDirName, i32x3, i32, CapFDManage, "report a preopen dir name (none: EBADF)"},
		{"proc_exit", e.procExit, i32, nil, CapProcessExit, "terminate the program with an exit code"},
		{"args_sizes_get", e.argsSizesGet, i32x2, i32, CapArgumentsRead, "report argc and argv byte size"},
		{"args_get", e.argsGet, i32x2, i32, CapArgumentsRead, "write argv pointers and bytes"},
		{"environ_sizes_get", e.environSizesGet, i32x2, i32, CapEnvironmentRead, "report environ count and byte size"},
		{"environ_get", e.environGet, i32x2, i32, CapEnvironmentRead, "write environ pointers and bytes"},
		{"clock_time_get", e.clockTimeGet, []wago.ValType{i32v, i64, i32v}, i32, CapClockRead, "read a clock's current time"},
		{"clock_res_get", e.clockResGet, i32x2, i32, CapClockRead, "read a clock's resolution"},
		{"random_get", e.randomGet, i32x2, i32, CapRandomRead, "fill a buffer with random bytes"},

		{"sched_yield", e.schedYield, nil, i32, CapSchedulerYield, "yield execution"},
		{"fd_advise", e.fdAdvise, []wago.ValType{i32v, i64, i64, i32v}, i32, CapFDManage, "provide file access advice"},
		{"fd_allocate", e.fdAllocate, []wago.ValType{i32v, i64, i64}, i32, CapFDWrite, "allocate file space"},
		{"fd_datasync", e.fdDatasync, i32, i32, CapFDWrite, "synchronize file data"},
		{"fd_sync", e.fdSync, i32, i32, CapFDWrite, "synchronize a file"},
		{"fd_fdstat_set_flags", e.fdFdstatSetFlags, i32x2, i32, CapFDManage, "set descriptor flags"},
		{"fd_fdstat_set_rights", e.fdFdstatSetRights, []wago.ValType{i32v, i64, i64}, i32, CapFDManage, "reduce descriptor rights"},
		{"fd_filestat_get", e.fdFilestatGet, i32x2, i32, CapFDRead, "get file metadata"},
		{"fd_filestat_set_size", e.fdFilestatSetSize, []wago.ValType{i32v, i64}, i32, CapFDWrite, "set file size"},
		{"fd_filestat_set_times", e.fdFilestatSetTimes, []wago.ValType{i32v, i64, i64, i32v}, i32, CapFDWrite, "set file timestamps"},
		{"fd_pread", e.fdPread, []wago.ValType{i32v, i32v, i32v, i64, i32v}, i32, CapFDRead, "read at an offset"},
		{"fd_pwrite", e.fdPwrite, []wago.ValType{i32v, i32v, i32v, i64, i32v}, i32, CapFDWrite, "write at an offset"},
		{"fd_readdir", e.fdReaddir, []wago.ValType{i32v, i32v, i32v, i64, i32v}, i32, CapFDRead, "read directory entries"},
		{"fd_renumber", e.fdRenumber, i32x2, i32, CapFDManage, "renumber a descriptor"},
		{"fd_tell", e.fdTell, i32x2, i32, CapFDManage, "get a descriptor offset"},
		{"path_create_directory", e.pathCreateDirectory, i32x3, i32, CapPathWrite, "create a directory"},
		{"path_filestat_get", e.pathFilestatGet, []wago.ValType{i32v, i32v, i32v, i32v, i32v}, i32, CapPathRead, "get path metadata"},
		{"path_filestat_set_times", e.pathFilestatSetTimes, []wago.ValType{i32v, i32v, i32v, i32v, i64, i64, i32v}, i32, CapPathWrite, "set path timestamps"},
		{"path_link", e.pathLink, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v, i32v}, i32, CapPathWrite, "create a hard link"},
		{"path_open", e.pathOpen, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i64, i64, i32v, i32v}, i32, CapPathWrite, "open or create a path"},
		{"path_readlink", e.pathReadlink, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v}, i32, CapPathRead, "read a symbolic link"},
		{"path_remove_directory", e.pathRemoveDirectory, i32x3, i32, CapPathWrite, "remove a directory"},
		{"path_rename", e.pathRename, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v}, i32, CapPathWrite, "rename a path"},
		{"path_symlink", e.pathSymlink, []wago.ValType{i32v, i32v, i32v, i32v, i32v}, i32, CapPathWrite, "create a symbolic link"},
		{"path_unlink_file", e.pathUnlinkFile, i32x3, i32, CapPathWrite, "unlink a file"},
		{"poll_oneoff", e.pollOneoff, i32x4, i32, CapPoll, "wait for events"},
		{"proc_raise", e.procRaise, i32, i32, CapUnsupported, "raise a signal (unsupported)"},
		{"sock_accept", e.sockAccept, i32x3, i32, CapUnsupported, "accept a socket (unsupported)"},
		{"sock_recv", e.sockRecv, []wago.ValType{i32v, i32v, i32v, i32v, i32v, i32v}, i32, CapUnsupported, "receive from a socket (unsupported)"},
		{"sock_send", e.sockSend, []wago.ValType{i32v, i32v, i32v, i32v, i32v}, i32, CapUnsupported, "send to a socket (unsupported)"},
		{"sock_shutdown", e.sockShutdown, i32x2, i32, CapUnsupported, "shut down a socket (unsupported)"},
	}
	for i := range bindings {
		fn := bindings[i].fn
		bindings[i].fn = func(m wago.HostModule, p, r []uint64) {
			e.withFS(m, r, func() { fn(m, p, r) })
		}
	}
	return bindings
}

func validatePluginConfig(raw json.RawMessage) error {
	_, err := decodePluginConfig(raw)
	return err
}

func decodePluginConfig(raw json.RawMessage) (pluginConfig, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > maxConfigBytes {
		return pluginConfig{}, fmt.Errorf("wasi: config exceeds %d bytes", maxConfigBytes)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return pluginConfig{}, fmt.Errorf("wasi: config must be a JSON object")
	}
	if !utf8.Valid(trimmed) {
		return pluginConfig{}, fmt.Errorf("wasi: config is not valid UTF-8")
	}
	if err := rejectDuplicateJSONKeys(trimmed); err != nil {
		return pluginConfig{}, fmt.Errorf("wasi: config: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return pluginConfig{}, fmt.Errorf("wasi: config: %w", err)
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return pluginConfig{}, fmt.Errorf("wasi: config field %q must not be null", name)
		}
	}
	var cfg pluginConfig
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return pluginConfig{}, fmt.Errorf("wasi: config: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return pluginConfig{}, fmt.Errorf("wasi: config has a trailing JSON value")
	}
	if _, err := configFromPluginConfig(cfg); err != nil {
		return pluginConfig{}, err
	}
	return cfg, nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value func() error
	value = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]struct{}{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate object key %q", key)
				}
				seen[key] = struct{}{}
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for dec.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delim)
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		wantEnd := json.Delim('}')
		if delim == '[' {
			wantEnd = ']'
		}
		if end != wantEnd {
			return fmt.Errorf("mismatched JSON delimiter %q", end)
		}
		return nil
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func configFromPluginConfig(cfg pluginConfig) (Config, error) {
	resolved := Config{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Env: os.Environ(), Now: func() int64 { return time.Now().UnixNano() },
		MaxOpenFiles: 1024, MaxPollDuration: time.Second,
	}
	if err := applyInputMode(&resolved, cfg.Stdin); err != nil {
		return Config{}, err
	}
	if err := applyOutputMode("stdout", &resolved.Stdout, cfg.Stdout); err != nil {
		return Config{}, err
	}
	if err := applyOutputMode("stderr", &resolved.Stderr, cfg.Stderr); err != nil {
		return Config{}, err
	}
	if cfg.Env != nil {
		if len(*cfg.Env) > 4096 {
			return Config{}, fmt.Errorf("wasi: env has %d entries, max 4096", len(*cfg.Env))
		}
		resolved.Env = append([]string(nil), (*cfg.Env)...)
		for _, entry := range resolved.Env {
			name, _, ok := strings.Cut(entry, "=")
			if !ok || name == "" || strings.ContainsRune(entry, 0) || len(entry) > 32768 {
				return Config{}, fmt.Errorf("wasi: invalid environment entry %q", entry)
			}
		}
	}
	if cfg.Preopens != nil {
		if len(*cfg.Preopens) > 64 {
			return Config{}, fmt.Errorf("wasi: preopens has %d entries, max 64", len(*cfg.Preopens))
		}
		resolved.Preopens = make(map[string]string, len(*cfg.Preopens))
		for guest, host := range *cfg.Preopens {
			if len(guest) == 0 || len(guest) > 4096 || !strings.HasPrefix(guest, "/") || path.Clean(guest) != guest || strings.ContainsRune(guest, 0) {
				return Config{}, fmt.Errorf("wasi: invalid guest preopen path %q", guest)
			}
			if len(host) == 0 || len(host) > 4096 || !filepath.IsAbs(host) || filepath.Clean(host) != host || strings.ContainsRune(host, 0) {
				return Config{}, fmt.Errorf("wasi: preopen %q requires a clean absolute host path", guest)
			}
			resolved.Preopens[guest] = host
		}
	}
	if cfg.MaxOpenFiles != nil {
		if *cfg.MaxOpenFiles < 3 || *cfg.MaxOpenFiles > 65536 {
			return Config{}, fmt.Errorf("wasi: maxOpenFiles must be between 3 and 65536")
		}
		resolved.MaxOpenFiles = *cfg.MaxOpenFiles
	}
	if cfg.MaxPollDurationMillis != nil {
		if *cfg.MaxPollDurationMillis < 1 || *cfg.MaxPollDurationMillis > 60000 {
			return Config{}, fmt.Errorf("wasi: maxPollDurationMillis must be between 1 and 60000")
		}
		resolved.MaxPollDuration = time.Duration(*cfg.MaxPollDurationMillis) * time.Millisecond
	}
	return resolved, nil
}

func applyInputMode(cfg *Config, mode *string) error {
	if mode == nil || *mode == "inherit" {
		return nil
	}
	if *mode == "eof" {
		cfg.Stdin = nil
		return nil
	}
	return fmt.Errorf("wasi: unsupported stdin mode %q", *mode)
}

func applyOutputMode(name string, dst *io.Writer, mode *string) error {
	if mode == nil || *mode == "inherit" {
		return nil
	}
	if *mode == "discard" {
		*dst = io.Discard
		return nil
	}
	return fmt.Errorf("wasi: unsupported %s mode %q", name, *mode)
}

func cloneConfig(cfg Config) Config {
	cfg.Args = append([]string(nil), cfg.Args...)
	cfg.Env = append([]string(nil), cfg.Env...)
	if cfg.Preopens != nil {
		preopens := make(map[string]string, len(cfg.Preopens))
		for guest, host := range cfg.Preopens {
			preopens[guest] = host
		}
		cfg.Preopens = preopens
	}
	return cfg
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

func (e *Plugin) fdWrite(m wago.HostModule, p, r []uint64) {
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

func (e *Plugin) fdRead(m wago.HostModule, p, r []uint64) {
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

func (e *Plugin) fdClose(_ wago.HostModule, p, r []uint64) {
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

func (e *Plugin) fdSeek(m wago.HostModule, p, r []uint64) {
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

func (e *Plugin) fdFdstatGet(m wago.HostModule, p, r []uint64) {
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

func (e *Plugin) fdPrestatGet(m wago.HostModule, p, r []uint64) {
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

func (e *Plugin) fdPrestatDirName(m wago.HostModule, p, r []uint64) {
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

func (e *Plugin) procExit(_ wago.HostModule, p, r []uint64) {
	panic(wago.HostExit{Code: int32(uint32(p[0]))})
}

func (e *Plugin) argsSizesGet(m wago.HostModule, p, r []uint64) {
	r[0] = writeCounts(m.Memory(), uint32(p[0]), uint32(p[1]), e.cfg.Args)
}

func (e *Plugin) argsGet(m wago.HostModule, p, r []uint64) {
	r[0] = writeStrings(m.Memory(), uint32(p[0]), uint32(p[1]), e.cfg.Args)
}

func (e *Plugin) environSizesGet(m wago.HostModule, p, r []uint64) {
	r[0] = writeCounts(m.Memory(), uint32(p[0]), uint32(p[1]), e.cfg.Env)
}

func (e *Plugin) environGet(m wago.HostModule, p, r []uint64) {
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

func (e *Plugin) clockTimeGet(m wago.HostModule, p, r []uint64) {
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
func (e *Plugin) clockResGet(m wago.HostModule, p, r []uint64) {
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

func (e *Plugin) randomGet(m wago.HostModule, p, r []uint64) {
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
