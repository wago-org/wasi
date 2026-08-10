package register

import (
	"errors"
	"testing"

	wago "github.com/wago-org/wago"
	"github.com/wago-org/wago/src/component"
	"github.com/wago-org/wasi/p2"
)

func TestWASIPluginRequiresScopedHostGrants(t *testing.T) {
	if err := wago.NewRuntime().LoadPlugins([]wago.PluginConfig{{
		Name:         "github.com/wago-org/wasi",
		Capabilities: []wago.PluginCapability{wago.PluginHostImports},
	}}); !errors.Is(err, wago.ErrPermissionDenied) {
		t.Fatalf("LoadPlugins without host.environment = %v, want permission denial", err)
	}

	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins([]wago.PluginConfig{{
		Name: "github.com/wago-org/wasi",
		Capabilities: []wago.PluginCapability{
			wago.PluginHostImports,
			wago.PluginHostEnvironment,
			wago.PluginInstanceHooks,
		},
	}}); err != nil {
		t.Fatalf("LoadPlugins with WASI grants: %v", err)
	}
	if _, ok := rt.HostImports()["wasi_snapshot_preview1.fd_write"]; !ok {
		t.Fatal("WASI fd_write was not registered")
	}
}

func TestWASIP2ServiceOrdersComponentProvider(t *testing.T) {
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins([]wago.PluginConfig{
		{Name: p2.ID},
		{Name: component.PluginID, Capabilities: []wago.PluginCapability{wago.PluginCoreEngine}},
	}); err != nil {
		t.Fatalf("LoadPlugins consumer before provider: %v", err)
	}
	if _, ok := rt.Extension(p2.ID); !ok {
		t.Fatal("WASI P2 consumer was not loaded")
	}
	if _, ok := rt.Extension(component.PluginID); !ok {
		t.Fatal("Component Model provider was not loaded")
	}
}
