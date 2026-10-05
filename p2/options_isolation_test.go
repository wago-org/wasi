package p2_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wago"
	wagoplugin "github.com/wago-org/wago/plugin"
	"github.com/wago-org/wasi/p2"
)

func optionsTestService(t testing.TB) (*wago.Runtime, *wagoplugin.Ref[component.Service]) {
	t.Helper()
	var ref *wagoplugin.Ref[component.Service]
	providers := []wago.PluginProvider{component.Provider(), componentConsumer(&ref)}
	rt := wago.NewRuntime()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, nil)); err != nil {
		rt.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	return rt, ref
}

func callCommand(in *component.Instance) error {
	_, err := in.CallExport(context.Background(), "wasi:cli/run@0.2.0", "run")
	return err
}

func TestReusedOptionsIsolateTablesAndDestructors(t *testing.T) {
	for _, closeSecond := range []bool{false, true} {
		name := "both-live"
		if closeSecond {
			name = "after-second-close"
		}
		t.Run(name, func(t *testing.T) {
			_, ref := optionsTestService(t)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("isolation\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := p2.Config{Mounts: []p2.Preopen{{GuestPath: "/data", HostPath: dir, Read: true, Write: true, MutateDirectory: true}}}
			opts := p2.Options(cfg)
			// Options snapshots mount authority when the caller creates the slice.
			cfg.Mounts[0].HostPath = filepath.Join(dir, "missing")
			err := ref.With(func(service component.Service) error {
				return service.WithInstance(context.Background(), rustFilesystem, func(first *component.Instance) error {
					if err := service.WithInstance(context.Background(), rustFilesystem, func(second *component.Instance) error {
						// Acquire the first guest's preopen after both resource hooks ran.
						if !closeSecond {
							if err := callCommand(first); err != nil {
								return err
							}
						}
						return callCommand(second)
					}, opts...); err != nil {
						return err
					}
					// Closing the second instance runs all of its descriptor destructors;
					// the first instance must still have its own table and filesystem state.
					if closeSecond {
						return callCommand(first)
					}
					return nil
				}, opts...)
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReusedOptionsConcurrentInstances(t *testing.T) {
	_, ref := optionsTestService(t)
	opts := p2.Options(p2.Config{Limits: p2.Limits{MaxPollables: 1}})
	err := ref.With(func(service component.Service) error {
		start, execute := make(chan struct{}), make(chan struct{})
		ready, results := make(chan error, 2), make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				entered := false
				err := service.WithInstance(context.Background(), rustSmoke, func(in *component.Instance) error {
					entered = true
					ready <- nil
					<-execute
					return callCommand(in)
				}, opts...)
				if !entered {
					ready <- err
				}
				results <- err
			}()
		}
		close(start)
		// Both independently created instances stay alive until their hooks ran,
		// then execute concurrently with a quota sufficient for each guest alone.
		var err error
		for i := 0; i < 2; i++ {
			err = errors.Join(err, <-ready)
		}
		close(execute)
		for i := 0; i < 2; i++ {
			err = errors.Join(err, <-results)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

var optionBenchmarkSink []component.Option

func BenchmarkOptions(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		optionBenchmarkSink = p2.Options(p2.Config{})
	}
}

func BenchmarkOptionsInstanceSetup(b *testing.B) {
	_, ref := optionsTestService(b)
	for _, reuse := range []bool{false, true} {
		name := "fresh-options"
		if reuse {
			name = "reused-options"
		}
		b.Run(name, func(b *testing.B) {
			opts := p2.Options(p2.Config{})
			b.ReportAllocs()
			b.ResetTimer()
			err := ref.With(func(service component.Service) error {
				for i := 0; i < b.N; i++ {
					if !reuse {
						opts = p2.Options(p2.Config{})
					}
					if err := service.WithInstance(context.Background(), rustSmoke, func(*component.Instance) error { return nil }, opts...); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
		})
	}
}
