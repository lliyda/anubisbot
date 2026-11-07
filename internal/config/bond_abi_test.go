package config

import (
	"encoding/json"
	"testing"
)

func TestEnsureBondClaimABI(t *testing.T) {
	m := map[string]Preset{
		"lgns_burn": {
			Function: "claimAll",
			Method:   "claimAll",
			ABI:      json.RawMessage(`[{"type":"function","name":"claimAll","stateMutability":"nonpayable","inputs":[],"outputs":[]}]`),
		},
	}
	if !EnsureBondClaimABI(m) {
		t.Fatal("expected change")
	}
	if EnsureBondClaimABI(m) {
		t.Fatal("second pass should be no-op")
	}
	var arr []map[string]any
	if err := json.Unmarshal(m["lgns_burn"].ABI, &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 2 {
		t.Fatalf("want 2 funcs got %d", len(arr))
	}
}
