//go:build (darwin || linux) && (amd64 || arm64) && !race && !tinygo

package p1_test

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/p1"
)

var wasiBenchApps = []string{
	"markdown.wasm", "crcsum.wasm", "blake3sum.wasm", "base64x.wasm",
	"jsonproc.wasm", "script.wasm", "regexmatch.wasm", "bignum.wasm",
}

func BenchmarkWazeroWASICompile(b *testing.B) {
	ctx := context.Background()
	for _, name := range wasiBenchApps {
		name := name
		b.Run(name, func(b *testing.B) {
			r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigCompiler())
			defer r.Close(ctx)
			src := wasiBenchSource(b, name)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c, err := r.CompileModule(ctx, src)
				if err != nil {
					b.Fatal(err)
				}
				c.Close(ctx)
			}
		})
	}
}

func BenchmarkWazeroWASIInstantiate(b *testing.B) {
	ctx := context.Background()
	for _, name := range wasiBenchApps {
		name := name
		b.Run(name, func(b *testing.B) {
			r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigCompiler())
			defer r.Close(ctx)
			wasi_snapshot_preview1.MustInstantiate(ctx, r)
			c, err := r.CompileModule(ctx, wasiBenchSource(b, name))
			if err != nil {
				b.Fatal(err)
			}
			defer c.Close(ctx)
			cfg := wazero.NewModuleConfig().WithArgs(name).WithStdout(io.Discard)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m, err := r.InstantiateModule(ctx, c, cfg)
				if err != nil {
					b.Fatal(err)
				}
				m.Close(ctx)
			}
		})
	}
}

func BenchmarkWazeroWASIRun(b *testing.B) {
	ctx := context.Background()
	for _, name := range wasiBenchApps {
		name := name
		b.Run(name, func(b *testing.B) {
			r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigCompiler())
			defer r.Close(ctx)
			wasi_snapshot_preview1.MustInstantiate(ctx, r)
			c, err := r.CompileModule(ctx, wasiBenchSource(b, name))
			if err != nil {
				b.Fatal(err)
			}
			defer c.Close(ctx)
			cfg := wazero.NewModuleConfig().WithArgs(name).WithStdout(io.Discard)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m, err := r.InstantiateModule(ctx, c, cfg)
				if err != nil {
					b.Fatal(err)
				}
				m.Close(ctx)
			}
		})
	}
}

func wasiBenchSource(b *testing.B, name string) []byte {
	b.Helper()
	src, err := os.ReadFile("../../wago/bench/corpus/" + name)
	if err != nil {
		b.Skipf("%s not present: %v", name, err)
	}
	return src
}

func BenchmarkWASICompile(b *testing.B) {
	for _, name := range wasiBenchApps {
		name := name
		b.Run(name, func(b *testing.B) {
			src := wasiBenchSource(b, name)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c, err := wago.Compile(nil, src)
				if err != nil {
					b.Fatal(err)
				}
				_ = c
			}
		})
	}
}

func BenchmarkWASIInstantiate(b *testing.B) {
	for _, name := range wasiBenchApps {
		name := name
		b.Run(name, func(b *testing.B) {
			c, err := wago.Compile(nil, wasiBenchSource(b, name))
			if err != nil {
				b.Fatal(err)
			}
			imports := p1.Imports(p1.Config{Stdout: io.Discard, Args: []string{name}})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				in, err := wago.Instantiate(c, wago.InstantiateOptions{Imports: imports})
				if err != nil {
					b.Fatal(err)
				}
				in.Close()
			}
		})
	}
}

func BenchmarkWASIRun(b *testing.B) {
	for _, name := range wasiBenchApps {
		name := name
		b.Run(name, func(b *testing.B) {
			c, err := wago.Compile(nil, wasiBenchSource(b, name))
			if err != nil {
				b.Fatal(err)
			}
			imports := p1.Imports(p1.Config{Stdout: io.Discard, Args: []string{name}})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				in, err := wago.Instantiate(c, wago.InstantiateOptions{Imports: imports})
				if err != nil {
					b.Fatal(err)
				}
				_, err = in.Invoke("_start")
				in.Close()
				if err != nil {
					if _, ok := err.(*wago.ExitError); !ok {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
