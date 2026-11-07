package burnbond

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"anubisbot/internal/helixchain"
	"anubisbot/internal/tokenbal"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	selUserDeposits = "08f43333" // userDeposits(address,uint256)
	selGetClaimable = "6f5244b1" // getClaimable(address,uint256)
	daiDecimals     = 18
	depositWorkers  = 8
)

// Deposit is one bond position for a user.
type Deposit struct {
	Index           int    `json:"index"`
	BurnedAt        string `json:"burnedAt,omitempty"`
	PrincipalDAI    string `json:"principalDAI"`
	BurnedLGNS      string `json:"burnedLGNS"`
	ReleasedDAI     string `json:"releasedDAI"`
	ReleasedDisplay string `json:"releasedDisplay"`
	Claimed         string `json:"claimed"`
	Claimable       string `json:"claimable"`
	Status          int    `json:"status"`
}

// DepositsPage is the bond list for one contract.
type DepositsPage struct {
	Contract          string    `json:"contract"`
	ClaimableTotal    string    `json:"claimableTotal"`
	ClaimableTotalRaw string    `json:"claimableTotalRaw"`
	DepositCount      int       `json:"depositCount"`
	Deposits          []Deposit `json:"deposits"`
	Errors            []string  `json:"errors,omitempty"`
}

// ContractForTaskType returns bond contract for fixed_price / lgns_burn.
func ContractForTaskType(taskType string) (string, bool) {
	switch taskType {
	case "fixed_price":
		return FixedPrice, true
	case "lgns_burn":
		return LgnsBurn, true
	default:
		return "", false
	}
}

// IsBondTaskType reports whether task type is a burn bond claim.
func IsBondTaskType(taskType string) bool {
	_, ok := ContractForTaskType(taskType)
	return ok
}

// LoadDeposits lists user deposits on contract via userDeposits + getClaimable.
func LoadDeposits(ctx context.Context, cli *ethclient.Client, contract, user string) DepositsPage {
	page := DepositsPage{
		Contract: contract,
		Deposits: []Deposit{},
	}
	if cli == nil || !common.IsHexAddress(contract) || !common.IsHexAddress(user) {
		page.Errors = append(page.Errors, "rpc unavailable")
		return page
	}
	user = common.HexToAddress(user).Hex()
	contract = common.HexToAddress(contract).Hex()

	total, err := callUint(ctx, cli, contract, selTotalClaimable, padAddr(user))
	if err != nil {
		page.Errors = append(page.Errors, "getTotalClaimable: "+err.Error())
		page.ClaimableTotal = "—"
	} else {
		page.ClaimableTotalRaw = total.String()
		page.ClaimableTotal = tokenbal.Format(total, lgnsDecimals)
	}

	cnt, err := callUint(ctx, cli, contract, selDepositCount, padAddr(user))
	if err != nil {
		page.Errors = append(page.Errors, "depositCount: "+err.Error())
		return page
	}
	n := int(cnt.Uint64())
	if n < 0 {
		n = 0
	}
	page.DepositCount = n
	if n == 0 {
		return page
	}

	out := make([]Deposit, n)
	errs := make([]string, n)
	sem := make(chan struct{}, depositWorkers)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, e := loadOneDeposit(ctx, cli, contract, user, i)
			out[i] = d
			if e != nil {
				errs[i] = fmt.Sprintf("deposit[%d]: %s", i, e.Error())
			}
		}()
	}
	wg.Wait()
	page.Deposits = out
	for _, e := range errs {
		if e != "" {
			page.Errors = append(page.Errors, e)
		}
	}
	return page
}

func loadOneDeposit(ctx context.Context, cli *ethclient.Client, contract, user string, index int) (Deposit, error) {
	d := Deposit{
		Index:           index,
		PrincipalDAI:    "—",
		BurnedLGNS:      "—",
		ReleasedDAI:     "—",
		ReleasedDisplay: "—",
		Claimed:         "—",
		Claimable:       "—",
	}
	idxPad := common.LeftPadBytes(big.NewInt(int64(index)).Bytes(), 32)
	raw, err := callBytes(ctx, cli, contract, selUserDeposits, padAddr(user), idxPad)
	if err != nil {
		return d, err
	}
	if len(raw) < 32*9 {
		return d, fmt.Errorf("userDeposits: short return (%d)", len(raw))
	}
	words := make([]*big.Int, 9)
	for i := 0; i < 9; i++ {
		words[i] = new(big.Int).SetBytes(raw[i*32 : (i+1)*32])
	}
	principal := words[0]
	burned := words[2]
	released := words[3]
	claimed := words[4]
	ts := words[7]
	status := words[8]

	d.PrincipalDAI = tokenbal.Format(principal, daiDecimals)
	d.BurnedLGNS = tokenbal.Format(burned, lgnsDecimals)
	d.ReleasedDAI = tokenbal.Format(released, daiDecimals)
	d.ReleasedDisplay = tokenbal.Format(released, daiDecimals) + "/" + tokenbal.Format(principal, daiDecimals)
	d.Claimed = tokenbal.Format(claimed, lgnsDecimals)
	d.Status = int(status.Int64())
	if ts.Sign() > 0 && ts.IsInt64() {
		d.BurnedAt = time.Unix(ts.Int64(), 0).UTC().Format(time.RFC3339)
	}

	claimable, err := callUint(ctx, cli, contract, selGetClaimable, padAddr(user), idxPad)
	if err != nil {
		d.Claimable = "—"
		return d, fmt.Errorf("getClaimable: %w", err)
	}
	d.Claimable = tokenbal.Format(claimable, lgnsDecimals)
	return d, nil
}

func callBytes(ctx context.Context, cli *ethclient.Client, to, selector string, args ...[]byte) ([]byte, error) {
	data := common.Hex2Bytes(selector)
	for _, a := range args {
		data = append(data, a...)
	}
	return helixchain.Call(ctx, cli, to, data)
}
