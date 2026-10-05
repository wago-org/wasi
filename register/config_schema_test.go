package register

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wago-org/wasi/p1"
)

func TestPreview1ConfigUnicodeLengthBoundaries(t *testing.T) {
	provider := p1.Provider()
	for _, character := range []string{"a", "é", "😀"} {
		for _, count := range []int{32768, 32769} {
			raw, err := json.Marshal(map[string]any{"env": []string{"K=" + strings.Repeat(character, count-2)}})
			if err != nil {
				t.Fatal(err)
			}
			if err := provider.ValidateConfig(raw); (err == nil) != (count == 32768) {
				t.Errorf("environment with %d %q characters: accepted=%v", count, character, err == nil)
			}
		}
	}
	for _, field := range []string{"guest", "host"} {
		for _, count := range []int{4096, 4097} {
			mount := map[string]any{"guest": "/data", "host": filepath.Clean(os.TempDir())}
			prefix := "/"
			if field == "host" {
				prefix = filepath.Clean(os.TempDir()) + string(filepath.Separator)
			}
			mount[field] = prefix + strings.Repeat("é", count-utf8.RuneCountInString(prefix))
			raw, err := json.Marshal(map[string]any{"mounts": []any{mount}})
			if err != nil {
				t.Fatal(err)
			}
			if err := provider.ValidateConfig(raw); (err == nil) != (count == 4096) {
				t.Errorf("mount %s with %d characters: accepted=%v", field, count, err == nil)
			}
		}
	}
}

func TestPreview1ConfigRejectsSchemaShapes(t *testing.T) {
	provider := p1.Provider()
	host, err := json.Marshal(filepath.Clean(os.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"Stdin":"eof"}`),
		json.RawMessage(`{"Env":["K=value"]}`),
		json.RawMessage(`{"maxopenfiles":3}`),
		json.RawMessage(`{"maxiOVecs":1}`),
		json.RawMessage(`{"MaxSubscriptionsPerPoll":1}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","host":"/tmp","read":null}]}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","host":"/tmp","write":null}]}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","host":"/tmp","mutateDirectory":null}]}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","host":"/tmp","Read":true}]}`),
		json.RawMessage(`{"mounts":[{"Guest":"/data","host":"/tmp"}]}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","Host":"/tmp"}]}`),
		json.RawMessage(`{"mounts":[null]}`),
		json.RawMessage(`{"mounts":[{"guest":null,"host":"/tmp"}]}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","host":null}]}`),
		json.RawMessage(`{"mounts":[{"host":"/tmp"}]}`),
		json.RawMessage(`{"mounts":[{"guest":"/data"}]}`),
		json.RawMessage(`{"env":[null]}`),
		json.RawMessage(`{"env":[false]}`),
		json.RawMessage(`{"mounts":{}}`),
		json.RawMessage(`{"mounts":[[]]}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","host":"/tmp","read":"true"}]}`),
		json.RawMessage(`{"env":null}`),
		json.RawMessage(`{"mounts":null}`),
		json.RawMessage(`{"maxOpenFiles":null}`),
		json.RawMessage(`{"unknown":false}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","host":"/tmp","unknown":false}]}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","guest":"/other","host":"/tmp"}]}`),
	} {
		raw = []byte(strings.ReplaceAll(string(raw), `"/tmp"`, string(host)))
		if err := provider.ValidateConfig(raw); err == nil {
			t.Errorf("accepted schema-invalid config %s", raw)
		}
	}
}

func TestPreview1ConfigAcceptsOptionalShapes(t *testing.T) {
	provider := p1.Provider()
	for _, value := range []any{
		map[string]any{},
		map[string]any{"env": []string{"é="}, "mounts": []any{}},
		map[string]any{"stdin": "eof", "stdout": "discard", "stderr": "inherit", "maxOpenFiles": 3, "maxIOVecs": 65536, "maxSubscriptionsPerPoll": 1},
		map[string]any{"mounts": []any{map[string]any{"guest": "/data", "host": filepath.Clean(os.TempDir()), "read": false, "write": true, "mutateDirectory": false}}},
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.ValidateConfig(raw); err != nil {
			t.Errorf("rejected valid config %s: %v", raw, err)
		}
	}
}

func BenchmarkPreview1ValidateConfig(b *testing.B) {
	provider := p1.Provider()
	mount, err := json.Marshal(map[string]any{"mounts": []any{map[string]any{"guest": "/data", "host": filepath.Clean(os.TempDir()), "read": true}}})
	if err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"empty", json.RawMessage(`{}`)},
		{"environment", json.RawMessage(`{"env":["KEY=value","OTHER=data"],"stdout":"discard","maxOpenFiles":1024}`)},
		{"mount", mount},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := provider.ValidateConfig(tc.raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
