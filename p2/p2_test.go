package p2_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wago"
	wagoplugin "github.com/wago-org/wago/plugin"
	"github.com/wago-org/wasi/p2"
)

// Built from testdata/rust-smoke with rustc 1.97.1's wasm32-wasip2 target.
// It is a genuine component binary containing Rust's Preview 1-to-2 adapter.
//
//go:embed testdata/rust_smoke.component.wasm
var rustSmoke []byte

//go:embed testdata/rust_filesystem.component.wasm
var rustFilesystem []byte

//go:embed testdata/rust_sockets.component.wasm
var rustSockets []byte

type pluginFunc func(*wago.Registrar) error

func (f pluginFunc) Register(r *wago.Registrar) error { return f(r) }

type flushBuffer struct {
	bytes.Buffer
	flushes int
}

func (b *flushBuffer) Flush() error {
	b.flushes++
	return nil
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func testDefinition(id string) wago.PluginDefinition {
	return wago.PluginDefinition{ID: id, Version: "1.0.0", Provenance: wago.PluginProvenance{Repository: "https://" + id, License: "MIT"}}
}

func componentConsumer(ref **wagoplugin.Ref[component.Service]) wago.PluginProvider {
	d := testDefinition("example.com/tests/component-consumer")
	d.Requires = []wago.PluginRequirement{{ID: component.PluginID, Version: "^0.1.0"}}
	d.Consumes = []wago.ContractRequirement{{ID: component.Contract.ID(), Major: component.Contract.Major(), Mode: wago.ContractRequired}}
	return wago.PluginProvider{Definition: d, New: func() wago.Plugin {
		return pluginFunc(func(r *wago.Registrar) error {
			var err error
			*ref, err = wagoplugin.Require(r, component.Contract)
			return err
		})
	}}
}

func p2Consumer(ref **wagoplugin.Ref[p2.Service]) wago.PluginProvider {
	d := testDefinition("example.com/tests/p2-consumer")
	d.Requires = []wago.PluginRequirement{{ID: p2.ID, Version: "^0.2.0"}}
	d.Consumes = []wago.ContractRequirement{{ID: p2.Contract.ID(), Major: p2.Contract.Major(), Mode: wago.ContractRequired}}
	return wago.PluginProvider{Definition: d, New: func() wago.Plugin {
		return pluginFunc(func(r *wago.Registrar) error {
			var err error
			*ref, err = wagoplugin.Require(r, p2.Contract)
			return err
		})
	}}
}

func pluginSet(t *testing.T, providers []wago.PluginProvider, configs map[string]json.RawMessage) wago.PluginSet {
	t.Helper()
	set := wago.PluginSet{Providers: providers}
	for _, provider := range providers {
		digest, err := wago.DefinitionDigest(provider.Definition)
		if err != nil {
			t.Fatal(err)
		}
		s := wago.PluginSelection{ID: provider.Definition.ID, DefinitionDigest: digest, Direct: true, Dependencies: map[string]string{}, Config: configs[provider.Definition.ID]}
		for _, req := range provider.Definition.Requires {
			s.Dependencies[req.ID] = req.Version
		}
		for _, req := range provider.Definition.Authorities {
			s.Grants = append(s.Grants, wago.AuthorityGrant{Name: req.Name, Scope: req.Scope})
		}
		for _, req := range provider.Definition.Consumes {
			var owners []string
			for _, candidate := range providers {
				for _, spec := range candidate.Definition.Provides {
					if spec.ID == req.ID && spec.Major == req.Major {
						owners = append(owners, candidate.Definition.ID)
					}
				}
			}
			sort.Strings(owners)
			s.Contracts = append(s.Contracts, wago.ContractBinding{ID: req.ID, Major: req.Major, Providers: owners})
		}
		set.Selections = append(set.Selections, s)
	}
	return set
}

func TestRustWASIP2CommandRunsOnWago(t *testing.T) {
	var ref *wagoplugin.Ref[component.Service]
	providers := []wago.PluginProvider{component.Provider(), componentConsumer(&ref)}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, nil)); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr flushBuffer
	err := ref.With(func(service component.Service) error {
		return p2.Run(context.Background(), service, rustSmoke, p2.Config{
			Stdin: strings.NewReader("from-rust-stdin\n"), Stdout: &stdout, Stderr: &stderr,
			Args: []string{"alpha", "beta"}, Env: []string{"WAGO_FLAVOR=component"},
			WallClock: func() time.Time { return time.Unix(1_700_000_000, 0) },
		})
	})
	if err != nil {
		t.Fatalf("run Rust wasm32-wasip2 command: %v\nstdout=%q\nstderr=%q", err, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "args=alpha,beta;env=component;stdin=from-rust-stdin;map=2;clock=true\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "rust-wasip2-stderr\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if stdout.flushes == 0 || stderr.flushes == 0 {
		t.Fatalf("flushes = stdout:%d stderr:%d, want both streams flushed", stdout.flushes, stderr.flushes)
	}
}

func TestRustWASIP2FilesystemUsesOnlyMountedDirectory(t *testing.T) {
	var ref *wagoplugin.Ref[component.Service]
	providers := []wago.PluginProvider{component.Provider(), componentConsumer(&ref)}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, nil)); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/input.txt", []byte("hello filesystem\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := ref.With(func(service component.Service) error {
		return p2.Run(context.Background(), service, rustFilesystem, p2.Config{
			Stdout:   &stdout,
			Preopens: map[string]string{"/data": dir},
		})
	})
	if err != nil {
		t.Fatalf("run Rust filesystem component: %v\nstdout=%q", err, stdout.String())
	}
	if got, want := stdout.String(), "input=hello filesystem;entries=input.txt,output.txt\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	got, err := os.ReadFile(dir + "/output.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := "HELLO FILESYSTEM\n"; string(got) != want {
		t.Fatalf("output.txt = %q, want %q", got, want)
	}
}

func TestRustWASIP2FilesystemRejectsSymlinkEscape(t *testing.T) {
	var ref *wagoplugin.Ref[component.Service]
	providers := []wago.PluginProvider{component.Provider(), componentConsumer(&ref)}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, nil)); err != nil {
		t.Fatal(err)
	}

	mount := t.TempDir()
	outside := t.TempDir() + "/outside.txt"
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, mount+"/input.txt"); err != nil {
		t.Fatal(err)
	}
	err := ref.With(func(service component.Service) error {
		return p2.Run(context.Background(), service, rustFilesystem, p2.Config{Preopens: map[string]string{"/data": mount}})
	})
	if err == nil {
		t.Fatal("symlink escape unexpectedly succeeded")
	}
	got, readErr := os.ReadFile(outside)
	if readErr != nil || string(got) != "outside\n" {
		t.Fatalf("outside file after denied escape = %q, %v", got, readErr)
	}
}

func TestProviderRunsRustFilesystemWithConfiguredPreopen(t *testing.T) {
	var ref *wagoplugin.Ref[p2.Service]
	providers := []wago.PluginProvider{component.Provider(), p2.Provider(), p2Consumer(&ref)}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/input.txt", []byte("provider\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"stdout": "discard", "preopens": map[string]string{"/data": dir}})
	if err != nil {
		t.Fatal(err)
	}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, map[string]json.RawMessage{p2.ID: raw})); err != nil {
		t.Fatal(err)
	}
	if err := ref.With(func(service p2.Service) error { return service.Run(context.Background(), rustFilesystem) }); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dir + "/output.txt")
	if err != nil || string(got) != "PROVIDER\n" {
		t.Fatalf("provider output = %q, %v", got, err)
	}
}

func TestRustWASIP2SocketsFailClosedWithoutNetworking(t *testing.T) {
	var ref *wagoplugin.Ref[component.Service]
	providers := []wago.PluginProvider{component.Provider(), componentConsumer(&ref)}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, nil)); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := ref.With(func(service component.Service) error {
		return p2.Run(context.Background(), service, rustSockets, p2.Config{Stdout: &stdout})
	})
	if err != nil {
		t.Fatalf("run Rust sockets component: %v\nstdout=%q", err, stdout.String())
	}
	if got, want := stdout.String(), "tcp=permission-denied;udp=permission-denied;dns=permission-denied\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestProviderRunsRustCommandThroughBothContracts(t *testing.T) {
	var ref *wagoplugin.Ref[p2.Service]
	providers := []wago.PluginProvider{component.Provider(), p2.Provider(), p2Consumer(&ref)}
	configs := map[string]json.RawMessage{p2.ID: json.RawMessage(`{"stdin":"eof","stdout":"discard","stderr":"discard","env":["WAGO_FLAVOR=provider"]}`)}
	rt := wago.NewRuntime(wago.WithGuestArguments([]string{"rust-smoke", "through", "provider"}))
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, configs)); err != nil {
		t.Fatal(err)
	}
	if err := ref.With(func(service p2.Service) error { return service.Run(context.Background(), rustSmoke) }); err != nil {
		t.Fatal(err)
	}
}

func TestRustWASIP2RunsRepeatedlyAndConcurrently(t *testing.T) {
	var ref *wagoplugin.Ref[component.Service]
	providers := []wago.PluginProvider{component.Provider(), componentConsumer(&ref)}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, nil)); err != nil {
		t.Fatal(err)
	}

	run := func(service component.Service, label string) error {
		var stdout, stderr flushBuffer
		err := p2.Run(context.Background(), service, rustSmoke, p2.Config{
			Stdin: strings.NewReader(label + "\n"), Stdout: &stdout, Stderr: &stderr,
			Args: []string{label}, Env: []string{"WAGO_FLAVOR=stress"},
		})
		if err != nil {
			return err
		}
		want := fmt.Sprintf("args=%s;env=stress;stdin=%s;map=2;clock=true\n", label, label)
		if stdout.String() != want || stderr.String() != "rust-wasip2-stderr\n" {
			return fmt.Errorf("output stdout=%q stderr=%q, want stdout=%q", stdout.String(), stderr.String(), want)
		}
		return nil
	}

	if err := ref.With(func(service component.Service) error {
		for i := range 20 {
			if err := run(service, fmt.Sprintf("sequential-%d", i)); err != nil {
				return fmt.Errorf("sequential instance %d: %w", i, err)
			}
		}

		// One Rust command expands to a large adapter graph. Two simultaneous
		// graphs fit the provider's reviewed 64-instance capability scope.
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := run(service, fmt.Sprintf("concurrent-%d", i)); err != nil {
					errs <- fmt.Errorf("concurrent instance %d: %w", i, err)
				}
			}()
		}
		wg.Wait()
		close(errs)
		var joined error
		for err := range errs {
			joined = errors.Join(joined, err)
		}
		return joined
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRustWASIP2HostTrapReturnsAnError(t *testing.T) {
	var ref *wagoplugin.Ref[component.Service]
	providers := []wago.PluginProvider{component.Provider(), componentConsumer(&ref)}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, nil)); err != nil {
		t.Fatal(err)
	}

	want := errors.New("random source failed")
	err := ref.With(func(service component.Service) error {
		return p2.Run(context.Background(), service, rustSmoke, p2.Config{Random: failingReader{err: want}})
	})
	if err == nil || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("Run with failed random source = %v, want error containing %q", err, want)
	}
}

func TestDefinitionAndConfigAreStrict(t *testing.T) {
	d := p2.Definition()
	if d.ID != p2.ID || !reflect.DeepEqual(d.Provides, []wago.ContractSpec{p2.Contract.Spec()}) {
		t.Fatalf("definition = %#v", d)
	}
	if got := d.Requires; len(got) != 1 || got[0].ID != component.PluginID {
		t.Fatalf("requires = %#v", got)
	}
	if got := d.Consumes; len(got) != 1 || got[0].ID != component.Contract.ID() {
		t.Fatalf("consumes = %#v", got)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{"unknown":true}`), json.RawMessage(`{"stdin":"pipe"}`), json.RawMessage(`{"preopens":{"relative":"/tmp"}}`), json.RawMessage(`{"preopens":{"/data":"relative"}}`), json.RawMessage(`{} {}`)} {
		if err := p2.Provider().ValidateConfig(raw); err == nil {
			t.Fatalf("accepted invalid config %s", raw)
		}
	}
}
