package p2_test

import (
	"encoding/json"
	"testing"

	"github.com/wago-org/wasi/p2"
)

func TestConfigRejectsUnpairedSurrogateEscapes(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"env":["K=\ud800"]}`),
		json.RawMessage(`{"env":["K=\udc00"]}`),
		json.RawMessage(`{"env":["K=\ud800text"]}`),
		json.RawMessage(`{"env":["K=\ud800\u0041"]}`),
	} {
		if err := p2.Provider().ValidateConfig(raw); err == nil {
			t.Errorf("accepted unpaired surrogate escape: %s", raw)
		}
	}
	if err := p2.Provider().ValidateConfig(json.RawMessage(`{"env":["K=\ud83d\ude00"]}`)); err != nil {
		t.Fatalf("rejected valid surrogate pair: %v", err)
	}
	if err := p2.Provider().ValidateConfig(json.RawMessage(`{"env":["K=\\ud800"]}`)); err != nil {
		t.Fatalf("rejected literal backslash sequence: %v", err)
	}
}
