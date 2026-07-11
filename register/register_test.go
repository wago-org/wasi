package register

import (
	"errors"
	"testing"

	wago "github.com/wago-org/wago"
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
		},
	}}); err != nil {
		t.Fatalf("LoadPlugins with WASI grants: %v", err)
	}
	if _, ok := rt.HostImports()["wasi_snapshot_preview1.fd_write"]; !ok {
		t.Fatal("WASI fd_write was not registered")
	}
}
