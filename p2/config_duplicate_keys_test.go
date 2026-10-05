package p2_test

import (
	"encoding/json"
	"testing"

	"github.com/wago-org/wasi/p2"
)

func TestConfigRejectsDuplicateKeys(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"stdout":"inherit","stdout":"discard"}`),
		json.RawMessage(`{"mounts":[{"guest":"/data","guest":"/other","host":"/tmp"}]}`),
		json.RawMessage(`{"limits":{"maxDescriptors":1,"maxDescriptors":2}}`),
	} {
		if err := p2.Provider().ValidateConfig(raw); err == nil {
			t.Errorf("accepted config with duplicate key: %s", raw)
		}
	}
}
