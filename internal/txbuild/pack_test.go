package txbuild

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"anubisbot/internal/config"
	"anubisbot/internal/tasks"
)

func bondPreset() config.Preset {
	abi := json.RawMessage(`[
		{"type":"function","name":"claimAll","stateMutability":"nonpayable","inputs":[],"outputs":[]},
		{"type":"function","name":"claim","stateMutability":"nonpayable","inputs":[{"name":"index","type":"uint256"}],"outputs":[]}
	]`)
	return config.Preset{
		Function: "claimAll",
		Method:   "claimAll",
		ABI:      abi,
	}
}

func TestPackCalldata_ClaimAll(t *testing.T) {
	data, err := PackCalldata(bondPreset(), tasks.TaskParams{ClaimMode: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(data) != "d1058e59" {
		t.Fatalf("got %x want d1058e59", data)
	}
}

func TestPackCalldata_ClaimIndexZero(t *testing.T) {
	data, err := PackCalldata(bondPreset(), tasks.TaskParams{
		ClaimMode: "index",
		Args:      []string{"0"},
		ClaimArg:  0,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(data)
	want := "379607f5" + "0000000000000000000000000000000000000000000000000000000000000000"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestPackCalldata_ClaimIndexTwelve(t *testing.T) {
	data, err := PackCalldata(bondPreset(), tasks.TaskParams{
		ClaimMode: "index",
		Args:      []string{"12"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(data)
	want := "379607f5" + "000000000000000000000000000000000000000000000000000000000000000c"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestBuildCalldata_LegacyClaimZero(t *testing.T) {
	p := config.Preset{Method: "claimAll", Selector: "d1058e59"}
	data, err := BuildCalldata("lgns_burn", p, tasks.TaskParams{
		ClaimMode: "index",
		Args:      []string{"0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(data)
	want := "379607f5" + "0000000000000000000000000000000000000000000000000000000000000000"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestEnsureEffectiveFunctionName(t *testing.T) {
	p := bondPreset()
	if EffectiveFunctionName(p, tasks.TaskParams{}) != "claimAll" {
		t.Fatal("default should be claimAll")
	}
	if EffectiveFunctionName(p, tasks.TaskParams{ClaimMode: "index"}) != "claim" {
		t.Fatal("index should be claim")
	}
}
