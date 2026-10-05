package p2_test

import (
	"context"
	_ "embed"
	"fmt"
	"runtime"
	"strings"
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wasi/p2"
)

// Rebuild with: wasm-tools parse testdata/write_zeroes.wat -o testdata/write_zeroes.component.wasm
//
//go:embed testdata/write_zeroes.component.wasm
var writeZeroesComponent []byte

type zeroesOutput struct {
	writes int
	bytes  int
}

func (*zeroesOutput) CheckWrite() (uint64, error) { return 64, nil }
func (o *zeroesOutput) TryWrite(p []byte) error {
	for _, v := range p {
		if v != 0 {
			return fmt.Errorf("nonzero output byte: %d", v)
		}
	}
	o.writes++
	o.bytes += len(p)
	return nil
}
func (*zeroesOutput) BeginFlush() error                  { return nil }
func (*zeroesOutput) WaitWritable(context.Context) error { return nil }

func zeroesInstances(t testing.TB) func(func(*component.Instance, *zeroesOutput) error) error {
	t.Helper()
	_, ref := optionsTestService(t)
	return func(fn func(*component.Instance, *zeroesOutput) error) error {
		out := &zeroesOutput{}
		return ref.With(func(s component.Service) error {
			return s.WithInstance(context.Background(), writeZeroesComponent, func(in *component.Instance) error { return fn(in, out) }, p2.Options(p2.Config{Stdout: out, Limits: p2.Limits{MaxAggregateBufferBytes: 32}})...)
		})
	}
}
func grantZeroes(in *component.Instance) error {
	_, err := in.Call(context.Background(), "grant")
	return err
}
func callZeroes(in *component.Instance, n uint64) error {
	_, err := in.Call(context.Background(), "zeroes", n)
	return err
}

func TestWriteZeroesPermitAndLimit(t *testing.T) {
	instances := zeroesInstances(t)
	for _, tc := range []struct {
		name           string
		n              uint64
		grant, success bool
	}{{"no-permit", 1, false, false}, {"over-limit", 64 << 10, true, false}, {"accepted", 32, true, true}, {"zero-length", 0, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			err := instances(func(in *component.Instance, out *zeroesOutput) error {
				if tc.grant {
					if err := grantZeroes(in); err != nil {
						return err
					}
				}
				err := callZeroes(in, tc.n)
				if !tc.success {
					if err == nil || !strings.Contains(err.Error(), "permit") {
						return fmt.Errorf("rejected write = %v", err)
					}
					if out.writes != 0 {
						return fmt.Errorf("rejected write reached output")
					}
					return nil
				}
				if err != nil {
					return err
				}
				if out.writes != 1 || out.bytes != int(tc.n) {
					return fmt.Errorf("successful zeroes = %d writes/%d bytes", out.writes, out.bytes)
				}
				// A second write without check-write must trap even after a zero-length write.
				if err := callZeroes(in, 0); err == nil || !strings.Contains(err.Error(), "permit") {
					return fmt.Errorf("successful write retained permit: %v", err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestWriteZeroesRejectsBeforeAllocating(t *testing.T) {
	instances := zeroesInstances(t)
	reject := func(n uint64) error {
		return instances(func(in *component.Instance, out *zeroesOutput) error {
			if err := grantZeroes(in); err != nil {
				return err
			}
			if err := callZeroes(in, n); err == nil || !strings.Contains(err.Error(), "permit") {
				return fmt.Errorf("rejected write = %v", err)
			}
			if out.writes != 0 {
				return fmt.Errorf("rejected request reached output")
			}
			return nil
		})
	}
	// A host trap prevents re-entering that instance. Warm the service, then
	// measure eight fresh instances for each length through the same guest ABI.
	if err := reject(64); err != nil {
		t.Fatal(err)
	}
	if err := reject(64 << 10); err != nil {
		t.Fatal(err)
	}
	measure := func(n uint64) (uint64, error) {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := 0; i < 8; i++ {
			if err := reject(n); err != nil {
				return 0, err
			}
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / 8, nil
	}
	small, err := measure(64)
	if err != nil {
		t.Fatal(err)
	}
	large, err := measure(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fresh instance + rejected64byte=%dB/call; rejected64KiB=%dB/call", small, large)
	if large > small+16<<10 {
		t.Fatalf("rejected scalar length increased allocation by %dB/call", large-small)
	}
	smallAllocs := testing.AllocsPerRun(8, func() {
		if err := reject(64); err != nil {
			t.Fatal(err)
		}
	})
	largeAllocs := testing.AllocsPerRun(8, func() {
		if err := reject(64 << 10); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("fresh instance + rejected64byte=%.0falloc/call; rejected64KiB=%.0falloc/call", smallAllocs, largeAllocs)
	// These counts include fresh-instance setup and canonical u64 boxing;
	// the bounded B/op comparison above guards allocation of rejected contents.
}
func BenchmarkWriteZeroes(b *testing.B) {
	for _, tc := range []struct {
		name    string
		n       uint64
		success bool
	}{{"rejected64B", 64, false}, {"rejected64KiB", 64 << 10, false}, {"accepted32B", 32, true}} {
		b.Run(tc.name, func(b *testing.B) {
			instances := zeroesInstances(b)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				err := instances(func(in *component.Instance, _ *zeroesOutput) error {
					if err := grantZeroes(in); err != nil {
						return err
					}
					err := callZeroes(in, tc.n)
					if tc.success && err != nil {
						return err
					}
					if !tc.success && err == nil {
						return fmt.Errorf("oversize request succeeded")
					}
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
		})
	}
}

func BenchmarkWriteZeroesAcceptedLiveInstance(b *testing.B) {
	instances := zeroesInstances(b)
	err := instances(func(in *component.Instance, _ *zeroesOutput) error {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := grantZeroes(in); err != nil {
				return err
			}
			if err := callZeroes(in, 32); err != nil {
				return err
			}
		}
		b.StopTimer()
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
}
