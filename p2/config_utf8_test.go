package p2_test

import (
	"encoding/json"
	"testing"

	"github.com/wago-org/wasi/p2"
)

func TestConfigRejectsMalformedUTF8(t *testing.T) {
	raw := json.RawMessage(append([]byte(`{"env":["K=`), 0xff))
	raw = append(raw, []byte(`"]}`)...)
	if err := p2.Provider().ValidateConfig(raw); err == nil {
		t.Fatalf("accepted malformed UTF-8 config %q", raw)
	}
}
