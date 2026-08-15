// Package p2 implements the WASI Preview 2 command world for Wago's
// Component Model runtime.
package p2

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	component "github.com/wago-org/component-model"
	wago "github.com/wago-org/wago"
	wagoplugin "github.com/wago-org/wago/plugin"
)

const (
	// ID is the canonical Preview 2 provider ID.
	ID = "github.com/wago-org/wasi/p2"

	outputStreamResource  uint32 = 1
	inputStreamResource   uint32 = 2
	errorResource         uint32 = 3
	descriptorResource    uint32 = 4
	pollableResource      uint32 = 5
	terminalInResource    uint32 = 6
	terminalOutResource   uint32 = 7
	networkResource       uint32 = 9
	tcpSocketResource     uint32 = 10
	udpSocketResource     uint32 = 11
	resolveStreamResource uint32 = 12

	stdoutRep   uint32 = 1
	stderrRep   uint32 = 2
	stdinRep    uint32 = 3
	readyRep    uint32 = 1
	timerRepMin uint32 = 0x1000
	maxIOSize          = 16 << 20
)

const (
	ifaceEnvironment = "wasi:cli/environment@0.2.0"
	ifaceExit        = "wasi:cli/exit@0.2.0"
	ifaceStdin       = "wasi:cli/stdin@0.2.0"
	ifaceStdout      = "wasi:cli/stdout@0.2.0"
	ifaceStderr      = "wasi:cli/stderr@0.2.0"
	ifaceTermStdin   = "wasi:cli/terminal-stdin@0.2.0"
	ifaceTermStdout  = "wasi:cli/terminal-stdout@0.2.0"
	ifaceTermStderr  = "wasi:cli/terminal-stderr@0.2.0"
	ifaceStreams     = "wasi:io/streams@0.2.0"
	ifacePoll        = "wasi:io/poll@0.2.0"
	ifacePreopens    = "wasi:filesystem/preopens@0.2.0"
	ifaceFilesystem  = "wasi:filesystem/types@0.2.0"
	ifaceRandom      = "wasi:random/random@0.2.0"
	ifaceInsecure    = "wasi:random/insecure@0.2.0"
	ifaceSeed        = "wasi:random/insecure-seed@0.2.0"
	ifaceMonoClock   = "wasi:clocks/monotonic-clock@0.2.0"
	ifaceWallClock   = "wasi:clocks/wall-clock@0.2.0"
)

// Config supplies one command component's ambient Preview 2 values. Filesystem
// access is limited to explicitly configured preopens. Socket APIs fail with
// access-denied until a networking capability is added.
type Config struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Args, Env      []string
	WallClock      func() time.Time
	Random         io.Reader
	// Preopens maps absolute guest directory names to host directories. No
	// host directory is visible unless it is explicitly listed here.
	Preopens map[string]string
}

// Service runs a wasi:cli/command component with the provider's reviewed
// configuration.
type Service interface {
	Run(context.Context, []byte) error
}

// Contract is the typed Preview 2 command runner published by Provider.
var Contract = wagoplugin.NewContract[Service](ID+"/command", 1)

// ExitError reports a wasi:cli/exit request or an error result from run.
type ExitError struct{ Code uint32 }

func (e *ExitError) Error() string {
	return fmt.Sprintf("wasi p2: command exited with status %d", e.Code)
}

// Definition returns fresh immutable metadata for the Preview 2 provider.
func Definition() wago.PluginDefinition {
	return wago.PluginDefinition{
		ID:          ID,
		Name:        "WASI Preview 2",
		Version:     "0.1.2",
		Description: "WASI Preview 2 command-world execution with capability-scoped filesystems and fail-closed sockets.",
		Stability:   wago.Experimental,
		Compatibility: wago.Compatibility{
			Engines:   map[string]string{"wago": ">=0.1.0", "go": ">=1.22"},
			Platforms: []string{"darwin/arm64", "linux/amd64"},
		},
		Provenance: wago.PluginProvenance{
			Homepage:   "https://github.com/wago-org/wasi",
			Repository: "https://github.com/wago-org/wasi",
			License:    "Apache-2.0",
			Authors:    []string{"The Wago authors"},
		},
		Requires: []wago.PluginRequirement{
			{ID: component.PluginID, Version: "^0.1.0"},
		},
		Authorities: []wago.AuthorityRequest{{
			Name:   wago.AuthorityHostArgumentsRead,
			Mode:   wago.AuthorityRequired,
			Reason: "expose this runtime's immutable argv through wasi:cli/environment",
		}},
		ConfigSchema: configSchema(),
		Provides:     []wago.ContractSpec{Contract.Spec()},
		Consumes: []wago.ContractRequirement{{
			ID: component.Contract.ID(), Major: component.Contract.Major(), Mode: wago.ContractRequired,
		}},
	}
}

type pluginConfig struct {
	Stdin    *string            `json:"stdin,omitempty"`
	Stdout   *string            `json:"stdout,omitempty"`
	Stderr   *string            `json:"stderr,omitempty"`
	Env      *[]string          `json:"env,omitempty"`
	Preopens *map[string]string `json:"preopens,omitempty"`
}

func configSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"stdin":{"type":"string","enum":["inherit","eof"]},"stdout":{"type":"string","enum":["inherit","discard"]},"stderr":{"type":"string","enum":["inherit","discard"]},"env":{"type":"array","maxItems":4096,"items":{"type":"string","minLength":2,"maxLength":32768,"pattern":"^[^=\\u0000]+=[^\\u0000]*$"}},"preopens":{"type":"object","maxProperties":64,"propertyNames":{"type":"string","pattern":"^/(?:[^/\\u0000]+(?:/[^/\\u0000]+)*)?$","maxLength":4096},"additionalProperties":{"type":"string","minLength":1,"maxLength":4096}}}}`)
}

type providerPlugin struct {
	components *wagoplugin.Ref[component.Service]
	arguments  *wago.GuestArgumentsAccess
	cfg        Config
}

// Provider returns the side-effect-free Preview 2 catalog entry.
func Provider() wago.PluginProvider {
	return wago.PluginProvider{
		Definition:     Definition(),
		New:            func() wago.Plugin { return new(providerPlugin) },
		ValidateConfig: validateConfig,
	}
}

func validateConfig(raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var cfg pluginConfig
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("wasi p2: config: %w", err)
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return fmt.Errorf("wasi p2: config must be an object")
	}
	if cfg.Stdin != nil && *cfg.Stdin != "inherit" && *cfg.Stdin != "eof" ||
		cfg.Stdout != nil && *cfg.Stdout != "inherit" && *cfg.Stdout != "discard" ||
		cfg.Stderr != nil && *cfg.Stderr != "inherit" && *cfg.Stderr != "discard" {
		return fmt.Errorf("wasi p2: invalid stream configuration")
	}
	if cfg.Preopens != nil {
		if err := validatePreopens(*cfg.Preopens); err != nil {
			return err
		}
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("wasi p2: config has a trailing JSON value")
	}
	return nil
}

func (p *providerPlugin) Register(reg *wago.Registrar) error {
	var raw pluginConfig
	if err := reg.Config(&raw); err != nil {
		return err
	}
	var err error
	p.components, err = wagoplugin.Require(reg, component.Contract)
	if err != nil {
		return err
	}
	p.arguments, err = reg.GuestArguments()
	if err != nil {
		return err
	}
	p.cfg = Config{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Env: os.Environ()}
	if raw.Stdin != nil && *raw.Stdin == "eof" {
		p.cfg.Stdin = nil
	}
	if raw.Stdout != nil && *raw.Stdout == "discard" {
		p.cfg.Stdout = io.Discard
	}
	if raw.Stderr != nil && *raw.Stderr == "discard" {
		p.cfg.Stderr = io.Discard
	}
	if raw.Env != nil {
		p.cfg.Env = append([]string(nil), (*raw.Env)...)
	}
	if raw.Preopens != nil {
		p.cfg.Preopens = make(map[string]string, len(*raw.Preopens))
		for guest, host := range *raw.Preopens {
			p.cfg.Preopens[guest] = host
		}
	}
	return wagoplugin.Provide(reg, Contract, Service(p))
}

func (p *providerPlugin) Run(ctx context.Context, wasm []byte) error {
	args, err := p.arguments.Args()
	if err != nil {
		return err
	}
	cfg := p.cfg
	if len(args) > 0 {
		cfg.Args = append([]string(nil), args[1:]...)
	}
	return p.components.With(func(components component.Service) error { return Run(ctx, components, wasm, cfg) })
}

// Run instantiates and executes a wasi:cli/command through an already leased
// Component Model service.
func Run(ctx context.Context, components component.Service, wasm []byte, cfg Config) error {
	if components == nil {
		return fmt.Errorf("wasi p2: nil component service")
	}
	if err := validatePreopens(cfg.Preopens); err != nil {
		return err
	}
	return components.WithInstance(ctx, wasm, func(in *component.Instance) error {
		exports := in.InstanceExports()
		sort.Strings(exports)
		for _, name := range exports {
			if strings.HasPrefix(name, "wasi:cli/run@") {
				results, err := in.CallExport(ctx, name, "run")
				if err != nil {
					return err
				}
				if len(results) != 1 {
					return fmt.Errorf("wasi p2: run returned %d values, want 1", len(results))
				}
				rv, ok := results[0].(component.ResultValue)
				if !ok {
					return fmt.Errorf("wasi p2: run returned %T, want result", results[0])
				}
				if rv.IsErr {
					return &ExitError{Code: 1}
				}
				return nil
			}
		}
		return fmt.Errorf("wasi p2: component does not export wasi:cli/run")
	}, Options(cfg)...)
}

func validatePreopens(preopens map[string]string) error {
	if len(preopens) > 64 {
		return fmt.Errorf("wasi p2: preopens has %d entries, max 64", len(preopens))
	}
	for guest, host := range preopens {
		if guest == "" || len(guest) > 4096 || !strings.HasPrefix(guest, "/") || path.Clean(guest) != guest || strings.ContainsRune(guest, 0) {
			return fmt.Errorf("wasi p2: invalid guest preopen path %q", guest)
		}
		if host == "" || len(host) > 4096 || !filepath.IsAbs(host) || filepath.Clean(host) != host || strings.ContainsRune(host, 0) {
			return fmt.Errorf("wasi p2: preopen %q requires a clean absolute host path", guest)
		}
	}
	return nil
}

type hostState struct {
	mu        sync.Mutex
	stdin     []byte
	stdinAt   int
	resources *component.HandleTable
	base      time.Time
	wall      func() time.Time
	deadlines map[uint32]time.Time
	nextTimer uint32
}

// Options returns Component Model host options for the Preview 2 command
// interfaces. Interface patch versions are matched by the component runtime.
func Options(cfg Config) []component.Option {
	stdout, stderr := cfg.Stdout, cfg.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	var stdin []byte
	var stdinErr error
	if cfg.Stdin != nil {
		stdin, stdinErr = io.ReadAll(io.LimitReader(cfg.Stdin, maxIOSize+1))
		if len(stdin) > maxIOSize {
			stdinErr = fmt.Errorf("stdin exceeds %d bytes", maxIOSize)
		}
	}
	wall := cfg.WallClock
	if wall == nil {
		wall = time.Now
	}
	random := cfg.Random
	if random == nil {
		random = crand.Reader
	}
	s := &hostState{stdin: stdin, base: time.Now(), wall: wall, deadlines: map[uint32]time.Time{}, nextTimer: timerRepMin}
	fs := newFilesystem(cfg.Preopens)

	getOutput := func(rep uint32) component.HostFunc {
		return func(context.Context, []component.Value) ([]component.Value, error) {
			return []component.Value{rep}, nil
		}
	}
	getStdin := func(context.Context, []component.Value) ([]component.Value, error) {
		if stdinErr != nil {
			return nil, fmt.Errorf("wasi:cli/stdin.get-stdin: %w", stdinErr)
		}
		return []component.Value{stdinRep}, nil
	}
	getArgs := func(context.Context, []component.Value) ([]component.Value, error) {
		out := make([]component.Value, 0, len(cfg.Args)+1)
		out = append(out, "wago")
		for _, arg := range cfg.Args {
			out = append(out, arg)
		}
		return []component.Value{out}, nil
	}
	getEnv := func(context.Context, []component.Value) ([]component.Value, error) {
		out := make([]component.Value, 0, len(cfg.Env))
		for _, entry := range cfg.Env {
			if k, v, ok := strings.Cut(entry, "="); ok {
				out = append(out, []component.Value{k, v})
			}
		}
		return []component.Value{out}, nil
	}
	exit := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("wasi:cli/exit.exit: expected 1 argument")
		}
		rv, ok := args[0].(component.ResultValue)
		if !ok {
			return nil, fmt.Errorf("wasi:cli/exit.exit: expected result, got %T", args[0])
		}
		if rv.IsErr {
			return nil, &ExitError{Code: 1}
		}
		return nil, &ExitError{Code: 0}
	}
	writer := func(rep uint32) (io.Writer, error) {
		switch rep {
		case stdoutRep:
			return stdout, nil
		case stderrRep:
			return stderr, nil
		}
		if w := fs.output(rep); w != nil {
			return w, nil
		}
		return nil, fmt.Errorf("wasi:io/streams: unknown output-stream rep %d", rep)
	}
	checkWrite := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("output-stream.check-write: expected self")
		}
		rep, ok := args[0].(uint32)
		if !ok {
			return nil, fmt.Errorf("output-stream.check-write: self is %T", args[0])
		}
		if _, err := writer(rep); err != nil {
			return nil, err
		}
		return []component.Value{component.ResultValue{Payload: uint64(1) << 40}}, nil
	}
	write := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("output-stream.write: expected self and contents")
		}
		rep, ok := args[0].(uint32)
		if !ok {
			return nil, fmt.Errorf("output-stream.write: self is %T", args[0])
		}
		buf, err := bytesValue(args[1])
		if err != nil {
			return nil, err
		}
		w, err := writer(rep)
		if err != nil {
			return nil, err
		}
		n, err := w.Write(buf)
		if err != nil {
			return nil, err
		}
		if n != len(buf) {
			return nil, io.ErrShortWrite
		}
		return []component.Value{component.ResultValue{}}, nil
	}
	flushWriter := func(rep uint32) error {
		w, err := writer(rep)
		if err != nil {
			return err
		}
		if f, ok := w.(interface{ Flush() error }); ok {
			return f.Flush()
		}
		return nil
	}
	blockingWriteAndFlush := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		result, err := write(ctx, args)
		if err != nil {
			return nil, err
		}
		rep, _ := args[0].(uint32) // write already validated the representation.
		if err := flushWriter(rep); err != nil {
			return nil, err
		}
		return result, nil
	}
	flush := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("output-stream.blocking-flush: expected self")
		}
		rep, ok := args[0].(uint32)
		if !ok {
			return nil, fmt.Errorf("output-stream.blocking-flush: self is %T", args[0])
		}
		if err := flushWriter(rep); err != nil {
			return nil, err
		}
		return []component.Value{component.ResultValue{}}, nil
	}
	read := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("input-stream.read: expected self and len")
		}
		rep, ok := args[0].(uint32)
		if !ok {
			return nil, fmt.Errorf("input-stream.read: unknown self")
		}
		n, ok := args[1].(uint64)
		if !ok {
			return nil, fmt.Errorf("input-stream.read: len is %T", args[1])
		}
		if n > maxIOSize {
			n = maxIOSize
		}
		if n == 0 {
			return []component.Value{component.ResultValue{Payload: []byte{}}}, nil
		}
		if rep != stdinRep {
			return fs.readStream(rep, n)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.stdinAt >= len(s.stdin) {
			return []component.Value{component.ResultValue{IsErr: true, Payload: component.VariantValue{Disc: 1}}}, nil
		}
		end := s.stdinAt + int(n)
		if end > len(s.stdin) {
			end = len(s.stdin)
		}
		out := append([]byte(nil), s.stdin[s.stdinAt:end]...)
		s.stdinAt = end
		return []component.Value{component.ResultValue{Payload: out}}, nil
	}
	getRandom := func(name string) component.HostFunc {
		return func(_ context.Context, args []component.Value) ([]component.Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("%s: expected len", name)
			}
			n, ok := args[0].(uint64)
			if !ok {
				return nil, fmt.Errorf("%s: len is %T", name, args[0])
			}
			if n > maxIOSize {
				return nil, fmt.Errorf("%s: length exceeds %d", name, maxIOSize)
			}
			b := make([]byte, int(n))
			if _, err := io.ReadFull(random, b); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			return []component.Value{b}, nil
		}
	}
	randU64 := func(name string) component.HostFunc {
		return func(context.Context, []component.Value) ([]component.Value, error) {
			var b [8]byte
			if _, err := io.ReadFull(random, b[:]); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			var v uint64
			for i := range b {
				v |= uint64(b[i]) << uint(8*i)
			}
			return []component.Value{v}, nil
		}
	}
	seed := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		a, e := randU64("insecure-seed")(ctx, args)
		if e != nil {
			return nil, e
		}
		b, e := randU64("insecure-seed")(ctx, args)
		if e != nil {
			return nil, e
		}
		return []component.Value{[]component.Value{a[0], b[0]}}, nil
	}

	opts := []component.Option{
		component.WithResourcesHook(func(t *component.HandleTable) { s.resources, fs.resources = t, t }),
		component.WithResourceTag(ifaceStreams, "output-stream", outputStreamResource),
		component.WithResourceTag(ifaceStreams, "input-stream", inputStreamResource),
		component.WithResourceTag("wasi:io/error@0.2.0", "error", errorResource),
		component.WithResourceTag(ifaceFilesystem, "descriptor", descriptorResource),
		component.WithResourceTag(ifacePoll, "pollable", pollableResource),
		component.WithImport(ifaceStdout, "get-stdout", getOutput(stdoutRep), nil, []component.TypeDesc{component.OwnDesc{ResourceType: outputStreamResource}}),
		component.WithImport(ifaceStderr, "get-stderr", getOutput(stderrRep), nil, []component.TypeDesc{component.OwnDesc{ResourceType: outputStreamResource}}),
		component.WithImport(ifaceStdin, "get-stdin", getStdin, nil, []component.TypeDesc{component.OwnDesc{ResourceType: inputStreamResource}}),
		component.WithImport(ifaceEnvironment, "get-arguments", getArgs, nil, []component.TypeDesc{component.ListDesc{Element: component.Prim("string")}}),
		component.WithImport(ifaceExit, "exit", exit, []component.TypeDesc{component.ResultDesc{}}, nil),
		custom(ifaceEnvironment, "get-environment", getEnv, func(t *component.TypeTable) component.FuncDesc {
			return t.Func(nil, t.List(t.Tuple(component.Prim("string"), component.Prim("string"))))
		}),
		custom(ifaceStreams, "[method]output-stream.check-write", checkWrite, checkWriteDesc),
		custom(ifaceStreams, "[method]output-stream.write", write, writeDesc),
		custom(ifaceStreams, "[method]output-stream.blocking-write-and-flush", blockingWriteAndFlush, writeDesc),
		custom(ifaceStreams, "[method]output-stream.blocking-flush", flush, flushDesc),
		custom(ifaceStreams, "[method]input-stream.read", read, inputReadDesc),
		custom(ifaceStreams, "[method]input-stream.blocking-read", read, inputReadDesc),
		component.WithImport(ifaceRandom, "get-random-bytes", getRandom("get-random-bytes"), []component.TypeDesc{component.PrimitiveDesc{Prim: "u64"}}, []component.TypeDesc{component.ListDesc{Element: component.Prim("u8")}}),
		component.WithImport(ifaceInsecure, "get-insecure-random-bytes", getRandom("get-insecure-random-bytes"), []component.TypeDesc{component.PrimitiveDesc{Prim: "u64"}}, []component.TypeDesc{component.ListDesc{Element: component.Prim("u8")}}),
		component.WithImport(ifaceRandom, "get-random-u64", randU64("get-random-u64"), nil, []component.TypeDesc{component.PrimitiveDesc{Prim: "u64"}}),
		component.WithImport(ifaceInsecure, "get-insecure-random-u64", randU64("get-insecure-random-u64"), nil, []component.TypeDesc{component.PrimitiveDesc{Prim: "u64"}}),
		component.WithImport(ifaceSeed, "insecure-seed", seed, nil, []component.TypeDesc{component.TupleDesc{Elements: []component.TypeRef{component.Prim("u64"), component.Prim("u64")}}}),
	}
	opts = append(opts, clockOptions(s)...)
	opts = append(opts, filesystemOptions(fs)...)
	opts = append(opts, socketOptions()...)
	// Non-TTY is a valid implementation of the terminal discovery interfaces.
	none := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{nil}, nil
	}
	opts = append(opts,
		component.WithResourceTag(ifaceTermStdin, "terminal-input", terminalInResource),
		component.WithResourceTag(ifaceTermStdout, "terminal-output", terminalOutResource),
		component.WithResourceTag(ifaceTermStderr, "terminal-output", terminalOutResource),
		terminalOption(ifaceTermStdin, "get-terminal-stdin", terminalInResource, none),
		terminalOption(ifaceTermStdout, "get-terminal-stdout", terminalOutResource, none),
		terminalOption(ifaceTermStderr, "get-terminal-stderr", terminalOutResource, none),
	)
	return opts
}

func terminalOption(iface, name string, resource uint32, fn component.HostFunc) component.Option {
	return custom(iface, name, fn, func(t *component.TypeTable) component.FuncDesc {
		return t.Func(nil, t.Option(t.Own(resource)))
	})
}
func custom(iface, name string, fn component.HostFunc, build func(*component.TypeTable) component.FuncDesc) component.Option {
	t := component.NewTypeTable()
	fd := build(t)
	return component.WithImportCustom(iface, name, fn, fd, t.Resolver())
}
func streamError(t *component.TypeTable) component.TypeRef {
	return t.Variant(component.VariantCaseSpec{Name: "last-operation-failed", Type: t.Own(errorResource)}, component.VariantCaseSpec{Name: "closed"})
}
func checkWriteDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(outputStreamResource)}, t.Result(component.Prim("u64"), streamError(t)))
}
func writeDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(outputStreamResource), t.List(component.Prim("u8"))}, t.Result(component.TypeRef{}, streamError(t)))
}
func flushDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(outputStreamResource)}, t.Result(component.TypeRef{}, streamError(t)))
}
func inputReadDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(inputStreamResource), component.Prim("u64")}, t.Result(t.List(component.Prim("u8")), streamError(t)))
}
func bytesValue(v component.Value) ([]byte, error) {
	if b, ok := v.([]byte); ok {
		return b, nil
	}
	xs, ok := v.([]component.Value)
	if !ok {
		return nil, fmt.Errorf("expected list<u8>, got %T", v)
	}
	b := make([]byte, len(xs))
	for i, x := range xs {
		u, ok := x.(uint32)
		if !ok {
			return nil, fmt.Errorf("list<u8>[%d] is %T", i, x)
		}
		b[i] = byte(u)
	}
	return b, nil
}
