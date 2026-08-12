package register

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	wago "github.com/wago-org/wago"
	wagoplugin "github.com/wago-org/wago/plugin"
	"github.com/wago-org/wasi"
	"github.com/wago-org/wasi/p1"
	"github.com/wago-org/wasi/unstable"
)

type pluginFunc func(*wago.Registrar) error

func (f pluginFunc) Register(reg *wago.Registrar) error { return f(reg) }

type pingService interface{ Ping() string }
type pingImplementation struct{}

func (pingImplementation) Ping() string { return "pong" }

var pingContract = wagoplugin.NewContract[pingService]("example.com/wasi-test/ping", 1)

func selection(t *testing.T, provider wago.PluginProvider, config json.RawMessage) wago.PluginSelection {
	t.Helper()
	digest, err := wago.DefinitionDigest(provider.Definition)
	if err != nil {
		t.Fatalf("DefinitionDigest(%s): %v", provider.Definition.ID, err)
	}
	grants := make([]wago.AuthorityGrant, len(provider.Definition.Authorities))
	for i, request := range provider.Definition.Authorities {
		grants[i] = wago.AuthorityGrant{Name: request.Name, Scope: request.Scope}
	}
	dependencies := make(map[string]string, len(provider.Definition.Requires))
	for _, requirement := range provider.Definition.Requires {
		dependencies[requirement.ID] = requirement.Version
	}
	return wago.PluginSelection{
		ID: provider.Definition.ID, DefinitionDigest: digest, Direct: true,
		Dependencies: dependencies, Grants: grants, Config: config,
	}
}

func TestProvidersAreExplicitFreshAndCanonical(t *testing.T) {
	first, second := Providers(), Providers()
	want := []string{wasi.ID, p1.ID, unstable.ID}
	if len(first) != len(want) || len(second) != len(want) {
		t.Fatalf("provider counts = %d, %d; want %d", len(first), len(second), len(want))
	}
	for i, id := range want {
		if first[i].Definition.ID != id {
			t.Fatalf("provider %d ID = %q, want %q", i, first[i].Definition.ID, id)
		}
	}
	first[0].Definition.ID = "mutated.example/plugin"
	if second[0].Definition.ID != wasi.ID {
		t.Fatal("Providers returned shared mutable definitions")
	}
}

func TestEachSnapshotLoadsWithExactAuthorities(t *testing.T) {
	for _, provider := range Providers() {
		provider := provider
		t.Run(provider.Definition.ID, func(t *testing.T) {
			rt := wago.NewRuntime(wago.WithGuestArguments([]string{"guest", "one"}))
			defer rt.Close()
			set := wago.PluginSet{
				Providers:  []wago.PluginProvider{provider},
				Selections: []wago.PluginSelection{selection(t, provider, nil)},
			}
			if err := rt.LoadPlugins(context.Background(), set); err != nil {
				t.Fatalf("LoadPlugins: %v", err)
			}
			imports := rt.ProvidedImports()
			if len(imports) == 0 || imports[0].Module == "" {
				t.Fatalf("ProvidedImports = %#v", imports)
			}
			for _, spec := range imports {
				if spec.Module != snapshotModule(provider.Definition.ID) {
					t.Fatalf("import %s module = %q", spec.Name, spec.Module)
				}
				if !spec.HasCapability || spec.Capability == "" {
					t.Fatalf("import %s has no guest capability", spec.Key())
				}
			}
		})
	}
}

func TestNarrowedHostScopeFailsClosed(t *testing.T) {
	provider := wasi.Provider()
	sel := selection(t, provider, nil)
	for i := range sel.Grants {
		if sel.Grants[i].Name == wago.AuthorityHostImportDefine {
			sel.Grants[i].Scope.Modules = nil
		}
	}
	err := wago.NewRuntime().LoadPlugins(context.Background(), wago.PluginSet{
		Providers: []wago.PluginProvider{provider}, Selections: []wago.PluginSelection{sel},
	})
	if err == nil {
		t.Fatal("LoadPlugins with empty module scope succeeded")
	}
}

func TestRootAndP1ConflictAtomically(t *testing.T) {
	root, versioned := wasi.Provider(), p1.Provider()
	rt := wago.NewRuntime()
	err := rt.LoadPlugins(context.Background(), wago.PluginSet{
		Providers: []wago.PluginProvider{root, versioned},
		Selections: []wago.PluginSelection{
			selection(t, root, nil), selection(t, versioned, nil),
		},
	})
	if !errors.Is(err, wago.ErrPluginConflict) {
		t.Fatalf("LoadPlugins(root+p1) = %v, want plugin conflict", err)
	}
	if got := rt.Plugins(); len(got) != 0 {
		t.Fatalf("failed transaction left plugins: %#v", got)
	}
}

func TestStrictConfigValidation(t *testing.T) {
	provider := wasi.Provider()
	tests := []json.RawMessage{
		json.RawMessage(`null`),
		json.RawMessage(`{"stdout":null}`),
		json.RawMessage(`{"stdout":"file"}`),
		json.RawMessage(`{"unknown":true}`),
		json.RawMessage(`{"stdout":"inherit","stdout":"discard"}`),
		json.RawMessage(`{"preopens":{"/data":"/srv/a","/data":"/srv/b"}}`),
		json.RawMessage(`{"maxOpenFiles":2}`),
		json.RawMessage(`{"preopens":{"/":"relative"}}`),
		json.RawMessage(`{"env":["NO_EQUALS"]}`),
		json.RawMessage(`{"env":["KEY=` + strings.Repeat("x", 256<<10) + `"]}`),
		json.RawMessage([]byte{'{', '"', 'e', 'n', 'v', '"', ':', '[', '"', 0xff, '"', ']', '}'}),
	}
	for _, config := range tests {
		if err := provider.ValidateConfig(config); err == nil {
			t.Fatalf("ValidateConfig(%s) succeeded", config)
		}
	}
	if err := provider.ValidateConfig(json.RawMessage(`{"stdin":"eof","stdout":"discard","stderr":"discard","env":[],"maxOpenFiles":3,"maxPollDurationMillis":1}`)); err != nil {
		t.Fatalf("valid config: %v", err)
	}
}

func TestMissingPreopenRollsBackPluginTransaction(t *testing.T) {
	provider := wasi.Provider()
	missing := filepath.Join(t.TempDir(), "missing")
	config, err := json.Marshal(map[string]any{"preopens": map[string]string{"/data": missing}})
	if err != nil {
		t.Fatal(err)
	}
	rt := wago.NewRuntime()
	err = rt.LoadPlugins(context.Background(), wago.PluginSet{
		Providers:  []wago.PluginProvider{provider},
		Selections: []wago.PluginSelection{selection(t, provider, config)},
	})
	if err == nil || !strings.Contains(err.Error(), "open preopen") {
		t.Fatalf("LoadPlugins with missing preopen = %v", err)
	}
	if got := rt.Plugins(); len(got) != 0 {
		t.Fatalf("startup rollback left plugins: %#v", got)
	}
	if got := rt.ProvidedImports(); len(got) != 0 {
		t.Fatalf("startup rollback left imports: %#v", got)
	}
}

func TestDefinitionsDoNotShareMutableMetadata(t *testing.T) {
	first, second := wasi.Definition(), wasi.Definition()
	first.Authorities[0].Scope.Modules[0] = "mutated"
	first.Compatibility.Engines["wago"] = "never"
	if reflect.DeepEqual(first, second) || second.Authorities[0].Scope.Modules[0] != wasi.Module || second.Compatibility.Engines["wago"] != ">=0.1.0" {
		t.Fatal("Definition returned shared mutable metadata")
	}
}

func TestWASICoexistsWithPluginDependenciesAndContractCalls(t *testing.T) {
	const (
		producerID = "example.com/wasi-test/producer"
		consumerID = "example.com/wasi-test/consumer"
	)
	producerDefinition := testDefinition(producerID)
	producerDefinition.Provides = []wago.ContractSpec{pingContract.Spec()}
	consumerDefinition := testDefinition(consumerID)
	consumerDefinition.Requires = []wago.PluginRequirement{{ID: producerID, Version: "^1.0.0"}}
	consumerDefinition.Consumes = []wago.ContractRequirement{{
		ID: pingContract.ID(), Major: pingContract.Major(), Mode: wago.ContractRequired,
	}}

	producer := wago.PluginProvider{
		Definition: producerDefinition,
		New: func() wago.Plugin {
			return pluginFunc(func(reg *wago.Registrar) error {
				return wagoplugin.Provide(reg, pingContract, pingService(pingImplementation{}))
			})
		},
	}
	var got string
	consumer := wago.PluginProvider{
		Definition: consumerDefinition,
		New: func() wago.Plugin {
			return pluginFunc(func(reg *wago.Registrar) error {
				ref, err := wagoplugin.Require(reg, pingContract)
				if err != nil {
					return err
				}
				return reg.Lifecycle(wago.PluginLifecycle{Start: func(context.Context) error {
					return ref.With(func(service pingService) error {
						got = service.Ping()
						return nil
					})
				}})
			})
		},
	}
	wasiProvider := wasi.Provider()
	consumerSelection := selection(t, consumer, nil)
	consumerSelection.Contracts = []wago.ContractBinding{{
		ID: pingContract.ID(), Major: pingContract.Major(), Providers: []string{producerID},
	}}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), wago.PluginSet{
		Providers: []wago.PluginProvider{consumer, wasiProvider, producer},
		Selections: []wago.PluginSelection{
			consumerSelection,
			selection(t, wasiProvider, nil),
			selection(t, producer, nil),
		},
	}); err != nil {
		t.Fatalf("LoadPlugins: %v", err)
	}
	if got != "pong" {
		t.Fatalf("cross-plugin call = %q, want pong", got)
	}
	plugins := rt.Plugins()
	if len(plugins) != 3 {
		t.Fatalf("Plugins = %#v", plugins)
	}
	order := map[string]int{}
	for i, definition := range plugins {
		order[definition.ID] = i
	}
	if order[producerID] >= order[consumerID] {
		t.Fatalf("dependency activation order = %#v", order)
	}
}

func testDefinition(id string) wago.PluginDefinition {
	return wago.PluginDefinition{
		ID: id, Name: id, Version: "1.0.0", Description: "contract integration test",
		Stability:     wago.Experimental,
		Compatibility: wago.Compatibility{Engines: map[string]string{"wago": ">=0.1.0"}},
		Provenance: wago.PluginProvenance{
			Repository: "https://" + id, License: "Apache-2.0", Authors: []string{"WASI test"},
		},
	}
}

func snapshotModule(id string) string {
	if id == unstable.ID {
		return unstable.Module
	}
	return wasi.Module
}
