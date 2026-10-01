package core

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/wago-org/wago"
)

func TestProviderShutdownWaitingFileCall(t *testing.T) {
	for repeat := 0; repeat < 32; repeat++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		rt, p, err := newOwnedRuntime(t, nil, ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		module, err := rt.Compile(dispatchTestModule("fd_fdstat_get", []wago.ValType{wago.ValI32, wago.ValI32}, []wago.ValType{wago.ValI32}))
		if err != nil {
			_ = rt.CloseContext(ctx)
			cancel()
			t.Fatal(err)
		}
		in, err := rt.Instantiate(ctx, module, ownedPolicy())
		if err != nil {
			_ = module.Close()
			_ = rt.CloseContext(ctx)
			cancel()
			t.Fatal(err)
		}
		if got, err := in.Invoke("run", 1, 0); err != nil || len(got) != 1 || got[0] != wasiOK {
			_ = in.Close()
			_ = module.Close()
			_ = rt.CloseContext(ctx)
			cancel()
			t.Fatalf("initial call: %v %v", got, err)
		}
		state := p.fs
		state.mu.Lock()
		called := make(chan struct{})
		go func() { defer close(called); _, _ = in.Invoke("run", 1, 0) }()
		for p.guard.mu.TryLock() {
			p.guard.mu.Unlock()
			if ctx.Err() != nil {
				state.mu.Unlock()
				<-called
				_ = module.Close()
				_ = rt.CloseContext(context.Background())
				cancel()
				t.Fatal("host call did not reach its filesystem lock")
			}
			runtime.Gosched()
		}
		closeErr := rt.Close()
		for i := 0; i < 8; i++ {
			runtime.Gosched()
		}
		state.mu.Unlock()
		<-called
		waitErr := rt.WaitClosed(ctx)
		_ = module.Close()
		cancel()
		if closeErr != nil || waitErr != nil {
			t.Fatalf("close: %v; wait: %v", closeErr, waitErr)
		}
		if state.fds != nil || p.fs != nil || p.guard.states != nil || p.stream.closes.Load() != 0 {
			t.Fatal("shutdown retained state or closed borrowed streams")
		}
	}
}

func TestProviderShutdownConcurrentFileCalls(t *testing.T) {
	for repeat := 0; repeat < 64; repeat++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		rt, p, err := newOwnedRuntime(t, nil, ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		module, err := rt.Compile(dispatchTestModule("fd_fdstat_get", []wago.ValType{wago.ValI32, wago.ValI32}, []wago.ValType{wago.ValI32}))
		if err != nil {
			_ = rt.CloseContext(ctx)
			cancel()
			t.Fatal(err)
		}
		var instances [4]*wago.Instance
		for i := range instances {
			instances[i], err = rt.Instantiate(ctx, module, ownedPolicy())
			if err != nil {
				_ = module.Close()
				_ = rt.CloseContext(ctx)
				cancel()
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		ready := make(chan struct{}, len(instances))
		var calls sync.WaitGroup
		for _, in := range instances {
			calls.Add(1)
			go func(in *wago.Instance) {
				defer calls.Done()
				result, err := in.Invoke("run", 1, 0)
				if err != nil || len(result) != 1 || result[0] != wasiOK {
					t.Errorf("initial file call: %v %v", result, err)
				}
				ready <- struct{}{}
				<-start
				for call := 0; call < 64; call++ {
					result, err := in.Invoke("run", 1, 0)
					if err != nil {
						return // Runtime shutdown can interrupt an admitted call.
					}
					if len(result) != 1 || result[0] != wasiOK && result[0] != wasiEBadf && result[0] != wasiEPerm {
						t.Errorf("file call during shutdown: %v", result)
						return
					}
				}
			}(in)
		}
		for range instances {
			<-ready
		}
		close(start)
		closeErr := rt.CloseContext(ctx)
		calls.Wait()
		_ = module.Close()
		cancel()
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if p.fs != nil || p.guard.states != nil || p.stream.closes.Load() != 0 {
			t.Fatal("shutdown retained state or closed borrowed streams")
		}
	}
}
