package register

import (
	"strings"
	"testing"

	"github.com/wago-org/component-model"
	"github.com/wago-org/wago"
	"github.com/wago-org/wasi/p2"
)

func TestWASIP2RequiresComponentProvider(t *testing.T) {
	if err := wago.NewRuntime().LoadPlugins([]wago.PluginConfig{{Name: p2.ID}}); err == nil || !strings.Contains(err.Error(), component.PluginID) {
		t.Fatalf("LoadPlugins without component provider = %v, want missing dependency", err)
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
