package burnbond

import (
	"math/big"
	"strings"
	"testing"

	"anubisbot/internal/tokenbal"
)

func TestComputeReleasedDAI_Jiangnan0(t *testing.T) {
	dai := mustBig("1922537440804747083466")
	cap := mustBig("23000")
	releasedRaw := mustBig("1061240667324220390061")
	last := mustBig("960000")
	acc := mustBig("1116000")

	released, total := computeReleasedDAI(dai, cap, releasedRaw, last, acc)
	if got := tokenbal.Format(total, 18); !strings.HasPrefix(got, "4421.836") {
		t.Fatalf("total=%s want prefix 4421.836", got)
	}
	if got := tokenbal.Format(released, 18); !strings.HasPrefix(got, "1361.156") {
		t.Fatalf("released=%s want prefix 1361.156", got)
	}
}

func TestComputeReleasedDAI_Xinxin0(t *testing.T) {
	dai := mustBig("10221164650214000000000")
	cap := mustBig("20000")
	releasedRaw := mustBig("817856710651523424000")
	last := mustBig("183370")
	acc := mustBig("190038")

	released, total := computeReleasedDAI(dai, cap, releasedRaw, last, acc)
	if got := tokenbal.Format(total, 18); !strings.HasPrefix(got, "20442.329") {
		t.Fatalf("total=%s want prefix 20442.329", got)
	}
	if got := tokenbal.Format(released, 18); !strings.HasPrefix(got, "886.011") {
		t.Fatalf("released=%s want prefix 886.011", got)
	}
}

func TestComputeReleasedDAI_Edges(t *testing.T) {
	dai := mustBig("1000000000000000000") // 1 DAI
	cap := mustBig("20000")
	raw := mustBig("500000000000000000") // 0.5
	last := mustBig("100")
	acc := mustBig("100") // no progress

	released, total := computeReleasedDAI(dai, cap, raw, last, acc)
	wantTotal := mustBig("2000000000000000000") // 2 DAI
	if total.Cmp(wantTotal) != 0 {
		t.Fatalf("total=%s want %s", total, wantTotal)
	}
	if released.Cmp(raw) != 0 {
		t.Fatalf("acc<=last: released=%s want raw %s", released, raw)
	}

	// Cap when released+delta exceeds total.
	acc2 := mustBig("100000000") // huge
	released2, total2 := computeReleasedDAI(dai, cap, raw, last, acc2)
	if released2.Cmp(total2) != 0 {
		t.Fatalf("capped released=%s total=%s", released2, total2)
	}

	released3, total3 := computeReleasedDAI(dai, big.NewInt(0), raw, last, acc)
	if total3.Sign() != 0 || released3.Sign() != 0 {
		t.Fatalf("zero capBps: released=%s total=%s", released3, total3)
	}

	released4, total4 := computeReleasedDAI(nil, nil, nil, nil, nil)
	if total4.Sign() != 0 || released4.Sign() != 0 {
		t.Fatalf("nil inputs: released=%s total=%s", released4, total4)
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
	if _, ok := n.SetString(s, 10); !ok {
		panic("bad big: " + s)
	}
	return n
}
