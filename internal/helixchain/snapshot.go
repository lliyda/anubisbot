package helixchain

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"anubisbot/internal/tokenbal"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	selStakeOf          = "42623360"
	selMinimumStake     = "ec5ffac2"
	selBurnEpochOf      = "99e02c2d"
	selDailyMaximumLgns = "020bb577"
	selBurnedLgnsToday  = "cb9ebaf2"
	selMaximumLgns      = "7300b78e"
	selCurrentEpoch     = "76671808"
	selAllowance        = "dd62ed3e"
	selTotalActivePower = "f2cfb069"
	selGetReserves      = "0902f1ac"
	selToken0           = "0dfe1681"
)

type Contracts struct {
	Gate   string
	Ledger string
	Clock  string
	Pair   string
	LGNS   string
	AiLGNS string
	DAI    string
}

type BurnGate struct {
	Stake        string  `json:"stake"`
	MinStake     string  `json:"minStake"`
	BurnEpoch    *int64  `json:"burnEpoch"`
	Epoch        *int64  `json:"epoch"`
	PerBurn      string  `json:"perBurn"`
	Allowance    string  `json:"allowance"`
	GasDai       string  `json:"gasDai"`
	MaxDaily     string  `json:"maxDaily"`
	BurnedToday  string  `json:"burnedToday"`
	QuotaLeft    string  `json:"quotaLeft"`
	OkStake      bool    `json:"okStake"`
	OkEpoch      bool    `json:"okEpoch"`
	OkBal        bool    `json:"okBal"`
	OkAllow      bool    `json:"okAllow"`
	OkGas        bool    `json:"okGas"`
	OkQuota      bool    `json:"okQuota"`
}

type Snapshot struct {
	BurnGate            *BurnGate
	LongStake           string
	NetworkActivePower  string
	AilgnsPriceDai      string
	Errs                []string
}

func Load(ctx context.Context, cli *ethclient.Client, c Contracts, holder string) Snapshot {
	out := Snapshot{}
	if cli == nil || holder == "" {
		out.Errs = append(out.Errs, "missing client or address")
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	addErr := func(msg string) {
		mu.Lock()
		out.Errs = append(out.Errs, msg)
		mu.Unlock()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		bg, stakeFmt, err := fetchBurnGate(ctx, cli, c, holder)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			out.Errs = append(out.Errs, "burnGate: "+err.Error())
			return
		}
		out.BurnGate = bg
		out.LongStake = stakeFmt
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if c.Ledger == "" {
			return
		}
		v, err := callUint(ctx, cli, c.Ledger, selTotalActivePower)
		if err != nil {
			addErr("totalActivePower: " + err.Error())
			return
		}
		mu.Lock()
		out.NetworkActivePower = tokenbal.Format(v, 18)
		mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if c.Pair == "" || c.AiLGNS == "" || c.DAI == "" {
			return
		}
		price, err := ailgnsPriceDai(ctx, cli, c.Pair, c.AiLGNS, c.DAI)
		if err != nil {
			addErr("ailgnsPrice: " + err.Error())
			return
		}
		mu.Lock()
		out.AilgnsPriceDai = price
		mu.Unlock()
	}()

	wg.Wait()
	return out
}

func fetchBurnGate(ctx context.Context, cli *ethclient.Client, c Contracts, holder string) (*BurnGate, string, error) {
	if c.Gate == "" || c.Clock == "" || c.LGNS == "" || c.Ledger == "" {
		return nil, "", fmt.Errorf("contracts incomplete")
	}
	holderPad := padAddr(holder)
	ledgerPad := padAddr(c.Ledger)

	type res struct {
		key string
		v   *big.Int
		err error
	}
	ch := make(chan res, 10)
	goRun := func(key, to, selHex string, args ...[]byte) {
		v, err := callUint(ctx, cli, to, selHex, args...)
		ch <- res{key, v, err}
	}
	go goRun("stake", c.Gate, selStakeOf, holderPad)
	go goRun("minStake", c.Gate, selMinimumStake)
	go goRun("burnEpoch", c.Gate, selBurnEpochOf, holderPad)
	go goRun("epoch", c.Clock, selCurrentEpoch)
	go goRun("lgns", c.LGNS, "70a08231", holderPad)
	go goRun("allow", c.LGNS, selAllowance, holderPad, ledgerPad)
	go goRun("maxDaily", c.Gate, selDailyMaximumLgns)
	go goRun("burnedToday", c.Gate, selBurnedLgnsToday)
	go goRun("perBurn", c.Gate, selMaximumLgns)

	vals := map[string]*big.Int{}
	var firstErr error
	for i := 0; i < 9; i++ {
		r := <-ch
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", r.key, r.err)
		}
		if r.err == nil {
			vals[r.key] = r.v
		}
	}
	if firstErr != nil && vals["stake"] == nil {
		return nil, "", firstErr
	}

	gasWei, err := cli.BalanceAt(ctx, common.HexToAddress(holder), nil)
	if err != nil {
		gasWei = big.NewInt(0)
	}

	stake := orZero(vals["stake"])
	minStake := orZero(vals["minStake"])
	perBurn := orZero(vals["perBurn"])
	if perBurn.Sign() == 0 {
		perBurn = new(big.Int).Exp(big.NewInt(10), big.NewInt(9), nil) // 1e9
		perBurn.Mul(perBurn, big.NewInt(500))
	}
	lgns := orZero(vals["lgns"])
	allow := orZero(vals["allow"])
	maxDaily := orZero(vals["maxDaily"])
	burnedToday := orZero(vals["burnedToday"])
	quota := new(big.Int).Sub(maxDaily, burnedToday)
	if quota.Sign() < 0 {
		quota = big.NewInt(0)
	}

	var burnEpoch, epoch *int64
	if v, ok := vals["burnEpoch"]; ok && v != nil {
		n := v.Int64()
		burnEpoch = &n
	}
	if v, ok := vals["epoch"]; ok && v != nil {
		n := v.Int64()
		epoch = &n
	}

	okEpoch := true
	if burnEpoch != nil && epoch != nil {
		okEpoch = *burnEpoch != *epoch
	}

	bg := &BurnGate{
		Stake:       tokenbal.Format(stake, 9),
		MinStake:    tokenbal.Format(minStake, 9),
		BurnEpoch:   burnEpoch,
		Epoch:       epoch,
		PerBurn:     tokenbal.Format(perBurn, 9),
		Allowance:   tokenbal.Format(allow, 9),
		GasDai:      tokenbal.Format(gasWei, 18),
		MaxDaily:    tokenbal.Format(maxDaily, 9),
		BurnedToday: tokenbal.Format(burnedToday, 9),
		QuotaLeft:   tokenbal.Format(quota, 9),
		OkStake:     stake.Cmp(minStake) >= 0 && minStake.Sign() > 0,
		OkEpoch:     okEpoch,
		OkBal:       lgns.Cmp(perBurn) >= 0,
		OkAllow:     allow.Cmp(perBurn) >= 0,
		OkGas:       gasWei.Cmp(new(big.Int).Mul(big.NewInt(2), new(big.Int).Exp(big.NewInt(10), big.NewInt(16), nil))) >= 0, // 0.02e18
		OkQuota:     quota.Cmp(perBurn) >= 0,
	}
	if minStake.Sign() == 0 {
		bg.OkStake = false
	}
	return bg, bg.Stake, nil
}

func orZero(v *big.Int) *big.Int {
	if v == nil {
		return big.NewInt(0)
	}
	return v
}

func ailgnsPriceDai(ctx context.Context, cli *ethclient.Client, pair, ailgns, dai string) (string, error) {
	_ = dai
	out, err := Call(ctx, cli, pair, sel(selToken0))
	if err != nil {
		return "", err
	}
	t0 := common.BytesToAddress(out)

	resOut, err := Call(ctx, cli, pair, sel(selGetReserves))
	if err != nil {
		return "", err
	}
	if len(resOut) < 64 {
		return "", fmt.Errorf("bad reserves")
	}
	r0 := new(big.Int).SetBytes(resOut[0:32])
	r1 := new(big.Int).SetBytes(resOut[32:64])

	ail := strings.ToLower(common.HexToAddress(ailgns).Hex())
	var reserveAi, reserveDai *big.Int
	if strings.ToLower(t0.Hex()) == ail {
		reserveAi, reserveDai = r0, r1
	} else {
		reserveAi, reserveDai = r1, r0
	}
	if reserveAi.Sign() == 0 {
		return "", fmt.Errorf("zero ailgns reserve")
	}
	// Both AiLGNS and DAI use 18 decimals on Anubis: price = reserveDai/reserveAi
	price18 := new(big.Int).Mul(reserveDai, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	price18.Div(price18, reserveAi)
	return tokenbal.Format(price18, 18), nil
}
