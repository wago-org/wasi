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

	outputStreamResource     uint32 = 1
	inputStreamResource      uint32 = 2
	errorResource            uint32 = 3
	descriptorResource       uint32 = 4
	pollableResource         uint32 = 5
	terminalInResource       uint32 = 6
	terminalOutResource      uint32 = 7
	networkResource          uint32 = 9
	tcpSocketResource        uint32 = 10
	udpSocketResource        uint32 = 11
	resolveStreamResource    uint32 = 12
	incomingDatagramResource uint32 = 13
	outgoingDatagramResource uint32 = 14

	stdoutRep uint32 = 1
	stderrRep uint32 = 2
	stdinRep  uint32 = 3
	maxIOSize        = 16 << 20
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
	Stdin          InputStream
	Stdout, Stderr OutputStream
	// Args is the complete argument vector, including argv[0].
	Args, Env []string
	WallClock func() time.Time
	Random    io.Reader
	// Mounts is the rights-aware preopen configuration. No rights are implied.
	// GuestPath and HostPath must be clean absolute paths.
	Mounts []Preopen
	Limits Limits
	// filesystem is populated transactionally by Run and remains private so
	// callers cannot bypass preopen validation.
	filesystem *filesystemState
}

// Limits bounds host resources owned by one component instance.
type Limits struct {
	MaxDescriptors          uint32 `json:"maxDescriptors,omitempty"`
	MaxStreams              uint32 `json:"maxStreams,omitempty"`
	MaxDirectoryStreams     uint32 `json:"maxDirectoryStreams,omitempty"`
	MaxPollables            uint32 `json:"maxPollables,omitempty"`
	MaxPollInputs           uint32 `json:"maxPollInputs,omitempty"`
	MaxDirectoryEntryBytes  uint64 `json:"maxDirectoryEntryBytes,omitempty"`
	MaxAggregateBufferBytes uint64 `json:"maxAggregateBufferBytes,omitempty"`
}

func (l Limits) normalized() Limits {
	if l.MaxDescriptors == 0 {
		l.MaxDescriptors = 256
	}
	if l.MaxStreams == 0 {
		l.MaxStreams = 256
	}
	if l.MaxDirectoryStreams == 0 {
		l.MaxDirectoryStreams = 64
	}
	if l.MaxPollables == 0 {
		l.MaxPollables = 1024
	}
	if l.MaxPollInputs == 0 {
		l.MaxPollInputs = 1024
	}
	if l.MaxDirectoryEntryBytes == 0 {
		l.MaxDirectoryEntryBytes = 1 << 20
	}
	if l.MaxAggregateBufferBytes == 0 {
		l.MaxAggregateBufferBytes = 16 << 20
	}
	return l
}
func (l Limits) ioLimit() uint64 {
	l = l.normalized()
	if l.MaxAggregateBufferBytes < maxIOSize {
		return l.MaxAggregateBufferBytes
	}
	return maxIOSize
}

// Preopen grants explicit filesystem rights beneath one host directory.
type Preopen struct {
	GuestPath       string `json:"guest"`
	HostPath        string `json:"host"`
	Read            bool   `json:"read"`
	Write           bool   `json:"write"`
	MutateDirectory bool   `json:"mutateDirectory"`
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
		Version:     "0.3.0",
		Description: "Experimental WASI 0.2 command host with fail-closed networking.",
		Stability:   wago.Experimental,
		Compatibility: wago.Compatibility{
			Engines:   map[string]string{"wago": ">=0.1.0", "go": ">=1.22"},
			Platforms: []string{"darwin/arm64", "linux/amd64", "linux/arm64"},
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
	Stdin  *string    `json:"stdin,omitempty"`
	Stdout *string    `json:"stdout,omitempty"`
	Stderr *string    `json:"stderr,omitempty"`
	Env    *[]string  `json:"env,omitempty"`
	Mounts *[]Preopen `json:"mounts,omitempty"`
	Limits *Limits    `json:"limits,omitempty"`
}

func configSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"stdin":{"type":"string","enum":["inherit","eof"]},"stdout":{"type":"string","enum":["inherit","discard"]},"stderr":{"type":"string","enum":["inherit","discard"]},"env":{"type":"array","maxItems":4096,"items":{"type":"string","minLength":2,"maxLength":32768,"pattern":"^[^=\\u0000]+=[^\\u0000]*$"}},"mounts":{"type":"array","maxItems":64,"items":{"type":"object","additionalProperties":false,"required":["guest","host"],"properties":{"guest":{"type":"string","pattern":"^/(?:[^/\\u0000]+(?:/[^/\\u0000]+)*)?$","maxLength":4096},"host":{"type":"string","minLength":1,"maxLength":4096},"read":{"type":"boolean"},"write":{"type":"boolean"},"mutateDirectory":{"type":"boolean"}}}},"limits":{"type":"object","additionalProperties":false,"properties":{"maxDescriptors":{"type":"integer","minimum":1,"maximum":65536},"maxStreams":{"type":"integer","minimum":1,"maximum":65536},"maxDirectoryStreams":{"type":"integer","minimum":1,"maximum":65536},"maxPollables":{"type":"integer","minimum":1,"maximum":65536},"maxPollInputs":{"type":"integer","minimum":1,"maximum":65536},"maxDirectoryEntryBytes":{"type":"integer","minimum":1,"maximum":16777216},"maxAggregateBufferBytes":{"type":"integer","minimum":1,"maximum":16777216}}}}}`)
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
	if cfg.Mounts != nil {
		if err := validateMounts(*cfg.Mounts); err != nil {
			return err
		}
	}
	if cfg.Limits != nil {
		if err := validateLimits(*cfg.Limits); err != nil {
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
	p.cfg = Config{Stdin: newInput(os.Stdin), Stdout: newOutput(os.Stdout), Stderr: newOutput(os.Stderr)}
	if raw.Stdin != nil && *raw.Stdin == "eof" {
		p.cfg.Stdin = nil
	}
	if raw.Stdout != nil && *raw.Stdout == "discard" {
		p.cfg.Stdout = newOutput(io.Discard)
	}
	if raw.Stderr != nil && *raw.Stderr == "discard" {
		p.cfg.Stderr = newOutput(io.Discard)
	}
	if raw.Env != nil {
		p.cfg.Env = append([]string(nil), (*raw.Env)...)
	}
	if raw.Mounts != nil {
		p.cfg.Mounts = append([]Preopen(nil), (*raw.Mounts)...)
	}
	if raw.Limits != nil {
		p.cfg.Limits = *raw.Limits
	}
	return wagoplugin.Provide(reg, Contract, Service(p))
}

func validateLimits(l Limits) error {
	values := []struct {
		name  string
		value uint64
		max   uint64
	}{{"MaxDescriptors", uint64(l.MaxDescriptors), 65536}, {"MaxStreams", uint64(l.MaxStreams), 65536}, {"MaxDirectoryStreams", uint64(l.MaxDirectoryStreams), 65536}, {"MaxPollables", uint64(l.MaxPollables), 65536}, {"MaxPollInputs", uint64(l.MaxPollInputs), 65536}, {"MaxDirectoryEntryBytes", l.MaxDirectoryEntryBytes, 16 << 20}, {"MaxAggregateBufferBytes", l.MaxAggregateBufferBytes, 16 << 20}}
	for _, v := range values {
		if v.value > v.max {
			return fmt.Errorf("wasi p2: %s exceeds %d", v.name, v.max)
		}
	}
	return nil
}

func (p *providerPlugin) Run(ctx context.Context, wasm []byte) error {
	args, err := p.arguments.Args()
	if err != nil {
		return err
	}
	cfg := p.cfg
	cfg.Args = append([]string(nil), args...)
	return p.components.With(func(components component.Service) error { return Run(ctx, components, wasm, cfg) })
}

// Run instantiates and executes a wasi:cli/command through an already leased
// Component Model service.
func Run(ctx context.Context, components component.Service, wasm []byte, cfg Config) error {
	if components == nil {
		return fmt.Errorf("wasi p2: nil component service")
	}
	if err := validateMounts(cfg.Mounts); err != nil {
		return err
	}
	filesystem, err := prepareFilesystem(cfg.Mounts, cfg.Limits)
	if err != nil {
		return err
	}
	defer filesystem.closeMounts()
	cfg.filesystem = filesystem
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

func validateMounts(mounts []Preopen) error {
	if len(mounts) > 64 {
		return fmt.Errorf("wasi p2: mounts has %d entries, max 64", len(mounts))
	}
	seen := make(map[string]struct{}, len(mounts))
	for _, mount := range mounts {
		guest, host := mount.GuestPath, mount.HostPath
		if guest == "" || len(guest) > 4096 || !strings.HasPrefix(guest, "/") || path.Clean(guest) != guest || strings.ContainsRune(guest, 0) {
			return fmt.Errorf("wasi p2: invalid guest mount path %q", guest)
		}
		if host == "" || len(host) > 4096 || !filepath.IsAbs(host) || filepath.Clean(host) != host || strings.ContainsRune(host, 0) {
			return fmt.Errorf("wasi p2: mount %q requires a clean absolute host path", guest)
		}
		if _, ok := seen[mount.GuestPath]; ok {
			return fmt.Errorf("wasi p2: duplicate guest mount path %q", mount.GuestPath)
		}
		seen[mount.GuestPath] = struct{}{}
	}
	return nil
}

type hostState struct {
	mu           sync.Mutex
	stdinMu      sync.Mutex
	stdin        InputStream
	stdout       OutputStream
	stderr       OutputStream
	resources    *component.HandleTable
	base         time.Time
	wall         func() time.Time
	errors       map[uint32]streamErrorValue
	nextError    uint32
	pollables    map[uint32]pollableValue
	nextPollable uint32
	permits      map[uint32]uint64
	outputs      map[uint32]OutputStream
	limits       Limits
}

// Options returns Component Model host options for the Preview 2 command
// interfaces. Interface patch versions are matched by the component runtime.
func Options(cfg Config) []component.Option {
	limits := cfg.Limits.normalized()
	stdin := cfg.Stdin
	if stdin == nil {
		stdin = newInput(nil)
	}
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = newOutput(nil)
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = newOutput(nil)
	}
	wall := cfg.WallClock
	if wall == nil {
		wall = time.Now
	}
	random := cfg.Random
	if random == nil {
		random = crand.Reader
	}
	s := &hostState{stdin: stdin, stdout: stdout, stderr: stderr, base: time.Now(), wall: wall, errors: map[uint32]streamErrorValue{}, nextError: 1, pollables: map[uint32]pollableValue{}, nextPollable: 1, permits: map[uint32]uint64{}, outputs: map[uint32]OutputStream{}, limits: limits}
	fs := cfg.filesystem
	if fs == nil {
		fs = newFilesystem(cfg.Mounts, limits)
	}
	fs.wall = wall
	fs.ioState = s

	getOutput := func(rep uint32) component.HostFunc {
		return func(context.Context, []component.Value) ([]component.Value, error) {
			return []component.Value{rep}, nil
		}
	}
	getStdin := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{stdinRep}, nil
	}
	getArgs := func(context.Context, []component.Value) ([]component.Value, error) {
		out := make([]component.Value, 0, len(cfg.Args))
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
	initialCWD := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{nil}, nil
	}
	errorDebug := func(_ context.Context, args []component.Value) ([]component.Value, error) {
		rep, err := repArg(args)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		value, ok := s.errors[rep]
		s.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("wasi:io/error: unknown error rep %d", rep)
		}
		return []component.Value{value.err.Error()}, nil
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
	writer := func(rep uint32) (OutputStream, error) {
		switch rep {
		case stdoutRep:
			return s.stdout, nil
		case stderrRep:
			return s.stderr, nil
		}
		s.mu.Lock()
		cached := s.outputs[rep]
		s.mu.Unlock()
		if cached != nil {
			return cached, nil
		}
		if w := fs.output(rep); w != nil {
			out := newOutput(w)
			s.mu.Lock()
			if prior := s.outputs[rep]; prior != nil {
				out = prior
			} else {
				s.outputs[rep] = out
			}
			s.mu.Unlock()
			return out, nil
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
		w, err := writer(rep)
		if err != nil {
			return nil, err
		}
		permit, err := w.CheckWrite()
		if err != nil {
			return s.streamFailure(err), nil
		}
		if permit > limits.ioLimit() {
			permit = limits.ioLimit()
		}
		s.mu.Lock()
		s.permits[rep] = permit
		s.mu.Unlock()
		return []component.Value{component.ResultValue{Payload: permit}}, nil
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
		s.mu.Lock()
		permit, granted := s.permits[rep]
		if granted {
			delete(s.permits, rep)
		}
		s.mu.Unlock()
		if !granted || uint64(len(buf)) > permit {
			return nil, fmt.Errorf("output-stream.write exceeds check-write permit")
		}
		if err := w.TryWrite(buf); err != nil {
			return s.streamFailure(err), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}
	flushWriter := func(rep uint32) error {
		w, err := writer(rep)
		if err != nil {
			return err
		}
		return w.BeginFlush()
	}
	blockingWriteAndFlush := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("output-stream.blocking-write-and-flush: expected self and contents")
		}
		rep, ok := args[0].(uint32)
		if !ok {
			return nil, fmt.Errorf("output-stream.blocking-write-and-flush: invalid self")
		}
		buf, err := bytesValue(args[1])
		if err != nil {
			return nil, err
		}
		if len(buf) > 4096 {
			return nil, fmt.Errorf("output-stream.blocking-write-and-flush: contents exceed 4096 bytes")
		}
		w, err := writer(rep)
		if err != nil {
			return nil, err
		}
		for len(buf) > 0 {
			if err := w.WaitWritable(ctx); err != nil {
				return nil, err
			}
			values, err := checkWrite(ctx, []component.Value{rep})
			if err != nil {
				return nil, err
			}
			rv := values[0].(component.ResultValue)
			if rv.IsErr {
				return values, nil
			}
			permit := rv.Payload.(uint64)
			if permit == 0 {
				continue
			}
			n := len(buf)
			if uint64(n) > permit {
				n = int(permit)
			}
			values, err = write(ctx, []component.Value{rep, buf[:n]})
			if err != nil {
				return nil, err
			}
			if values[0].(component.ResultValue).IsErr {
				return values, nil
			}
			buf = buf[n:]
		}
		if err := w.WaitWritable(ctx); err != nil {
			return nil, err
		}
		if err := w.BeginFlush(); err != nil {
			return s.streamFailure(err), nil
		}
		if err := w.WaitWritable(ctx); err != nil {
			return nil, err
		}
		return []component.Value{component.ResultValue{}}, nil
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
			return s.streamFailure(err), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}
	blockingFlush := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("output-stream.blocking-flush: expected self")
		}
		rep := args[0].(uint32)
		w, err := writer(rep)
		if err != nil {
			return nil, err
		}
		if err := w.WaitWritable(ctx); err != nil {
			return nil, err
		}
		if err := w.BeginFlush(); err != nil {
			return s.streamFailure(err), nil
		}
		if err := w.WaitWritable(ctx); err != nil {
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
		if n > limits.ioLimit() {
			n = limits.ioLimit()
		}
		if n == 0 {
			return []component.Value{component.ResultValue{Payload: []byte{}}}, nil
		}
		if rep != stdinRep {
			values, err := fs.readStream(rep, n)
			if err != nil {
				return s.streamFailure(err), nil
			}
			return values, nil
		}
		buf := make([]byte, int(n))
		s.stdinMu.Lock()
		got, err := s.stdin.TryRead(buf)
		s.stdinMu.Unlock()
		if errors.Is(err, ErrWouldBlock) {
			return []component.Value{component.ResultValue{Payload: []byte{}}}, nil
		}
		if err != nil && got == 0 {
			return s.streamFailure(err), nil
		}
		return []component.Value{component.ResultValue{Payload: buf[:got]}}, nil
	}
	blockingRead := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) == 2 {
			if rep, ok := args[0].(uint32); ok && rep == stdinRep {
				s.stdinMu.Lock()
				err := s.stdin.WaitReadable(ctx)
				s.stdinMu.Unlock()
				if err != nil {
					return nil, err
				}
			}
		}
		return read(ctx, args)
	}
	skip := func(blocking bool) component.HostFunc {
		return func(ctx context.Context, args []component.Value) ([]component.Value, error) {
			var values []component.Value
			var err error
			if blocking {
				values, err = blockingRead(ctx, args)
			} else {
				values, err = read(ctx, args)
			}
			if err != nil || len(values) != 1 {
				return values, err
			}
			rv, ok := values[0].(component.ResultValue)
			if !ok || rv.IsErr {
				return values, nil
			}
			buf, err := bytesValue(rv.Payload)
			if err != nil {
				return nil, err
			}
			return []component.Value{component.ResultValue{Payload: uint64(len(buf))}}, nil
		}
	}
	writeZeroes := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("output-stream.write-zeroes: expected self and len")
		}
		n, ok := args[1].(uint64)
		if !ok || n > maxIOSize {
			return nil, fmt.Errorf("output-stream.write-zeroes: invalid len")
		}
		return write(ctx, []component.Value{args[0], make([]byte, int(n))})
	}
	blockingWriteZeroes := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("output-stream.blocking-write-zeroes-and-flush: expected self and len")
		}
		n, ok := args[1].(uint64)
		if !ok || n > 4096 {
			return nil, fmt.Errorf("output-stream.blocking-write-zeroes-and-flush: invalid len")
		}
		return blockingWriteAndFlush(ctx, []component.Value{args[0], make([]byte, int(n))})
	}
	splice := func(blocking bool) component.HostFunc {
		return func(ctx context.Context, args []component.Value) ([]component.Value, error) {
			if len(args) != 3 {
				return nil, fmt.Errorf("output-stream.splice: expected self, src, len")
			}
			outRep, ok := args[0].(uint32)
			if !ok {
				return nil, fmt.Errorf("output-stream.splice: invalid self")
			}
			n, ok := args[2].(uint64)
			if !ok {
				return nil, fmt.Errorf("output-stream.splice: invalid len")
			}
			check, err := checkWrite(ctx, []component.Value{outRep})
			if err != nil {
				return nil, err
			}
			cr := check[0].(component.ResultValue)
			if cr.IsErr {
				return check, nil
			}
			permit := cr.Payload.(uint64)
			if n > permit {
				n = permit
			}
			var got []component.Value
			if blocking {
				got, err = blockingRead(ctx, []component.Value{args[1], n})
			} else {
				got, err = read(ctx, []component.Value{args[1], n})
			}
			if err != nil {
				return nil, err
			}
			rr := got[0].(component.ResultValue)
			if rr.IsErr {
				return got, nil
			}
			buf, e := bytesValue(rr.Payload)
			if e != nil {
				return nil, e
			}
			written, err := write(ctx, []component.Value{outRep, buf})
			if err != nil {
				return nil, err
			}
			wr := written[0].(component.ResultValue)
			if wr.IsErr {
				return written, nil
			}
			return []component.Value{component.ResultValue{Payload: uint64(len(buf))}}, nil
		}
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
			if n > limits.ioLimit() {
				return nil, fmt.Errorf("%s: length exceeds %d", name, limits.ioLimit())
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
		component.WithHostResourceDtor(errorResource, func(_ context.Context, rep uint32) error {
			s.mu.Lock()
			delete(s.errors, rep)
			s.mu.Unlock()
			return nil
		}),
		custom(ifaceStdout, "get-stdout", getOutput(stdoutRep), func(t *component.TypeTable) component.FuncDesc { return t.Func(nil, t.Own(outputStreamResource)) }),
		custom(ifaceStderr, "get-stderr", getOutput(stderrRep), func(t *component.TypeTable) component.FuncDesc { return t.Func(nil, t.Own(outputStreamResource)) }),
		custom(ifaceStdin, "get-stdin", getStdin, func(t *component.TypeTable) component.FuncDesc { return t.Func(nil, t.Own(inputStreamResource)) }),
		custom(ifaceEnvironment, "get-arguments", getArgs, func(t *component.TypeTable) component.FuncDesc { return t.Func(nil, t.List(component.Prim("string"))) }),
		custom(ifaceExit, "exit", exit, func(t *component.TypeTable) component.FuncDesc {
			return t.Func([]component.TypeRef{t.Result(component.TypeRef{}, component.TypeRef{})}, component.TypeRef{})
		}),
		custom(ifaceEnvironment, "get-environment", getEnv, func(t *component.TypeTable) component.FuncDesc {
			return t.Func(nil, t.List(t.Tuple(component.Prim("string"), component.Prim("string"))))
		}),
		custom(ifaceEnvironment, "initial-cwd", initialCWD, func(t *component.TypeTable) component.FuncDesc {
			return t.Func(nil, t.Option(component.Prim("string")))
		}),
		custom("wasi:io/error@0.2.0", "[method]error.to-debug-string", errorDebug, func(t *component.TypeTable) component.FuncDesc {
			return t.Func([]component.TypeRef{t.Borrow(errorResource)}, component.Prim("string"))
		}),
		custom(ifaceStreams, "[method]output-stream.check-write", checkWrite, checkWriteDesc),
		custom(ifaceStreams, "[method]output-stream.write", write, writeDesc),
		custom(ifaceStreams, "[method]output-stream.blocking-write-and-flush", blockingWriteAndFlush, writeDesc),
		custom(ifaceStreams, "[method]output-stream.blocking-flush", blockingFlush, flushDesc),
		custom(ifaceStreams, "[method]output-stream.flush", flush, flushDesc),
		custom(ifaceStreams, "[method]output-stream.write-zeroes", writeZeroes, writeZeroesDesc),
		custom(ifaceStreams, "[method]output-stream.blocking-write-zeroes-and-flush", blockingWriteZeroes, writeZeroesDesc),
		custom(ifaceStreams, "[method]output-stream.splice", splice(false), spliceDesc),
		custom(ifaceStreams, "[method]output-stream.blocking-splice", splice(true), spliceDesc),
		custom(ifaceStreams, "[method]input-stream.read", read, inputReadDesc),
		custom(ifaceStreams, "[method]input-stream.blocking-read", blockingRead, inputReadDesc),
		custom(ifaceStreams, "[method]input-stream.skip", skip(false), inputSkipDesc),
		custom(ifaceStreams, "[method]input-stream.blocking-skip", skip(true), inputSkipDesc),
		custom(ifaceRandom, "get-random-bytes", getRandom("get-random-bytes"), bytesRandomDesc),
		custom(ifaceInsecure, "get-insecure-random-bytes", getRandom("get-insecure-random-bytes"), bytesRandomDesc),
		custom(ifaceRandom, "get-random-u64", randU64("get-random-u64"), u64Result),
		custom(ifaceInsecure, "get-insecure-random-u64", randU64("get-insecure-random-u64"), u64Result),
		custom(ifaceSeed, "insecure-seed", seed, func(t *component.TypeTable) component.FuncDesc {
			return t.Func(nil, t.Tuple(component.Prim("u64"), component.Prim("u64")))
		}),
	}
	opts = append(opts, clockOptions(s, fs)...)
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
	recordSurface(iface, name)
	t := component.NewTypeTable()
	fd := build(t)
	return component.WithImportCustom(iface, name, fn, fd, t.Resolver())
}

var surfaceRecorder struct {
	sync.Mutex
	fn func(string, string)
}

func recordSurface(iface, name string) {
	surfaceRecorder.Lock()
	fn := surfaceRecorder.fn
	surfaceRecorder.Unlock()
	if fn != nil {
		fn(iface, name)
	}
}
func bytesRandomDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{component.Prim("u64")}, t.List(component.Prim("u8")))
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
func inputSkipDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(inputStreamResource), component.Prim("u64")}, t.Result(component.Prim("u64"), streamError(t)))
}
func writeZeroesDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(outputStreamResource), component.Prim("u64")}, t.Result(component.TypeRef{}, streamError(t)))
}
func spliceDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(outputStreamResource), t.Borrow(inputStreamResource), component.Prim("u64")}, t.Result(component.Prim("u64"), streamError(t)))
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
