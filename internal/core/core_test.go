package core

import (
	"reflect"
	"testing"

	wago "github.com/wago-org/wago"
)

func TestRegisterUsesHostEnvironmentArgsUnlessConfigured(t *testing.T) {
	wago.SetGuestArgs([]string{"guest", "one"})
	t.Cleanup(func() { wago.SetGuestArgs(nil) })

	e := New("test.wasi", wago.ExtensionInfo{ID: "test.wasi"}, Config{})
	if err := wago.NewRuntime().Use(e); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if got, want := e.cfg.Args, []string{"guest", "one"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("host argv = %v, want %v", got, want)
	}

	explicit := New("test.wasi.explicit", wago.ExtensionInfo{ID: "test.wasi.explicit"}, Config{Args: []string{"fixed"}})
	if err := wago.NewRuntime().Use(explicit); err != nil {
		t.Fatalf("Use explicit config: %v", err)
	}
	if got, want := explicit.cfg.Args, []string{"fixed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit argv = %v, want %v", got, want)
	}
}
