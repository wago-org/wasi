package register

import (
	"errors"
	"os/exec"
	"strings"
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
			wago.PluginInstanceHooks,
		},
	}}); err != nil {
		t.Fatalf("LoadPlugins with WASI grants: %v", err)
	}
	if _, ok := rt.HostImports()["wasi_snapshot_preview1.fd_write"]; !ok {
		t.Fatal("WASI fd_write was not registered")
	}
}

func TestDefaultRegisterExcludesComponentModel(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/wago-org/wasi/register").CombinedOutput()
	if err != nil {
		t.Fatalf("go list default register dependencies: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "github.com/wago-org/component-model") {
		t.Fatal("default Preview 1 registration unexpectedly retains component-model")
	}
}
