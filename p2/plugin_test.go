package p2

import (
	"context"
	"strings"
	"testing"

	"github.com/wago-org/wago/src/component"
	"github.com/wago-org/wago/src/wago"
)

func TestExtensionConsumesComponentServiceWithoutCoreAuthority(t *testing.T) {
	ext := NewExtension(Config{})
	if got := ext.Info().RequiresCapabilities; len(got) != 0 {
		t.Fatalf("WASI P2 core capabilities = %v, want none", got)
	}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.Use(ext); err == nil || !strings.Contains(err.Error(), component.ServiceName) {
		t.Fatalf("Use without component provider = %v", err)
	}
	if _, ok := rt.Extension(ID); ok {
		t.Fatal("failed service resolution mutated the runtime")
	}
}

func TestRuntimeFailsClosedAfterWagoRuntimeClose(t *testing.T) {
	rt := wago.NewRuntime()
	wasi, err := Enable(rt, Config{})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := wasi.Instantiate(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("Instantiate after close = %v, want inactive service", err)
	}
}

func TestNewExtensionCopiesAmbientStringSlices(t *testing.T) {
	args := []string{"before"}
	env := []string{"MODE=safe"}
	ext := NewExtension(Config{Args: args, Env: env})
	args[0], env[0] = "after", "MODE=unsafe"
	if ext.config.Args[0] != "before" || ext.config.Env[0] != "MODE=safe" {
		t.Fatalf("extension config aliases caller slices: args=%v env=%v", ext.config.Args, ext.config.Env)
	}
}
