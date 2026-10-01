package core

import (
	"context"
	"github.com/wago-org/wago"
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalMapProviderReuse(t *testing.T) {
	for _, mode := range []string{"normal", "trap", "start-trap"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "input"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			rt, p, err := newOwnedRuntime(t, []Preopen{{GuestPath: "/", HostPath: root, Read: true}}, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer rt.CloseContext(context.Background())
			first := p.fs
			module, err := rt.Compile(ownedCommandBytes(mode == "start-trap"))
			if err != nil {
				t.Fatal(err)
			}
			defer module.Close()
			in, err := rt.Instantiate(context.Background(), module, ownedPolicy())
			if mode == "start-trap" {
				if err == nil {
					in.Close()
					t.Fatal("start trap missing")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer in.Close()
				for i := 0; i < 128; i++ {
					if _, err := in.Invoke("run"); err != nil {
						t.Fatal(err)
					}
				}
				files := openOwnedFiles(p)
				if len(files) != 129 {
					t.Fatalf("descriptor growth: %d", len(files))
				}
				if mode == "trap" {
					if !in.Write(32, []byte("other")) {
						t.Fatal("write failed")
					}
					if _, err := in.Invoke("run"); err == nil {
						t.Fatal("missing trap")
					}
				}
				if err := in.Close(); err != nil {
					t.Fatal(err)
				}
				requireFilesClosed(t, files)
			}
			first.mu.Lock()
			released := first.fds == nil
			first.mu.Unlock()
			if !released {
				t.Fatal("first terminal map retained while provider is alive")
			}
			if p.fs != first {
				t.Fatal("provider availability marker changed")
			}
			nextModule, err := rt.Compile(ownedCommandBytes(false))
			if err != nil {
				t.Fatal(err)
			}
			defer nextModule.Close()
			next, err := rt.Instantiate(context.Background(), nextModule, ownedPolicy())
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			if _, err := next.Invoke("run"); err != nil {
				t.Fatal(err)
			}
			files := openOwnedFiles(p)
			if len(files) != 2 {
				t.Fatal("second instance failed to open independent resources")
			}
			if err := next.Close(); err != nil {
				t.Fatal(err)
			}
			requireFilesClosed(t, files)
			if p.stream.closes.Load() != 0 {
				t.Fatal("borrowed streams closed")
			}
		})
	}
}

func TestTerminalMapConcurrentIsolation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	rt, p, err := newOwnedRuntime(t, []Preopen{{GuestPath: "/", HostPath: root, Read: true}}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.CloseContext(context.Background())
	module, err := rt.Compile(ownedCommandBytes(false))
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close()
	var instances [4]*wago.Instance
	for i := range instances {
		instances[i], err = rt.Instantiate(context.Background(), module, ownedPolicy())
		if err != nil {
			t.Fatal(err)
		}
		defer instances[i].Close()
	}
	errs := make(chan error, 4)
	for _, in := range instances {
		go func(in *wago.Instance) {
			for j := 0; j < 16; j++ {
				if _, err := in.Invoke("run"); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}(in)
	}
	for range instances {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	files := openOwnedFiles(p)
	if len(files) != 68 {
		t.Fatalf("independent files: %d", len(files))
	}
	p.guard.mu.Lock()
	states := make([]*fsState, 0, len(p.guard.states))
	for _, s := range p.guard.states {
		states = append(states, s)
	}
	p.guard.mu.Unlock()
	for _, in := range instances[:3] {
		go func(in *wago.Instance) { errs <- in.Close() }(in)
	}
	if _, err := instances[3].Invoke("run"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	p.guard.mu.Lock()
	var live *fsState
	for _, s := range p.guard.states {
		live = s
	}
	remaining := len(p.guard.states)
	p.guard.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("remaining instances: %d", remaining)
	}
	for _, s := range states {
		s.mu.Lock()
		released := s.fds == nil
		s.mu.Unlock()
		if released == (s == live) {
			t.Fatal("cleanup crossed instance ownership")
		}
	}
	if err := instances[3].Close(); err != nil {
		t.Fatal(err)
	}
	requireFilesClosed(t, files)
	if p.stream.closes.Load() != 0 {
		t.Fatal("closed borrowed streams")
	}
}
