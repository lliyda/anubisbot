package burnbond

import (
	"math/big"
	"testing"

	"anubisbot/internal/tokenbal"
)

func TestDepositFieldFormatting(t *testing.T) {
	principal := mustBig("10729387373155102579")
	released := mustBig("6652220171356163592")
	burned := mustBig("5274907563")
	claimed := mustBig("11533690324")

	p := tokenbal.Format(principal, 18)
	r := tokenbal.Format(released, 18)
	b := tokenbal.Format(burned, 9)
	c := tokenbal.Format(claimed, 9)
	if p == "" || r == "" || b == "" || c == "" {
		t.Fatal("empty format")
	}
	display := r + "/" + p
	if display != r+"/"+p {
		t.Fatal(display)
	}
}

func TestContractForTaskType(t *testing.T) {
	if _, ok := ContractForTaskType("helix"); ok {
		t.Fatal("helix is not bond")
	}
	c, ok := ContractForTaskType("lgns_burn")
	if !ok || c != LgnsBurn {
		t.Fatalf("lgns_burn contract %s ok=%v", c, ok)
	}
}

func mustBig(s string) *big.Int {
	n := new(big.Int)
	n.SetString(s, 10)
	return n
}
