package burnbond

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"anubisbot/internal/helixchain"
	"anubisbot/internal/tokenbal"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	FixedPrice  = "0x3f403751975e1EB06d95901f7FF246513444E71F"
	LgnsBurn    = "0x11b10C9827c5B7071E96fcAa143B4e6E86b17c69"
	PayoutFixed = "0x9D46CB414E76237d3b83E2a1630481038368f217" // LGNS payout for fixed-price claims
	LGNSToken   = "0x4D1D808a081FdAc440703b3765FC61f8028C06B8"

	selTotalClaimable = "267e8ab6"
	selDepositCount   = "1bae91a4"

	lgnsDecimals      = 9
	defaultClaimLimit = 10
	maxClaimLimit     = 50
	transferLookback  = 100_000 // blocks
	maxLogRange       = 5_000   // rpc.anubispace.org eth_getLogs cap
)

var (
	claimedTopic  = crypto.Keccak256Hash([]byte("Claimed(address,uint256,uint256)"))
	transferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
)

// ClaimsPage is one page of burn claim history.
type ClaimsPage struct {
	Claims     []Claim  `json:"claims"`
	NextBefore uint64   `json:"nextBeforeBlock"`
	Source     string   `json:"source"` // count | rpc
	Errors     []string `json:"errors,omitempty"`
}

type Snapshot struct {
	FixedPrice BondView `json:"fixedPrice"`
	LgnsBurn   BondView `json:"lgnsBurn"`
	Claims     []Claim  `json:"claims"`
	Errors     []string `json:"errors,omitempty"`
}

type BondView struct {
	Label        string `json:"label"`
	Contract     string `json:"contract"`
	Claimable    string `json:"claimable"`
	ClaimableRaw string `json:"claimableRaw"`
	DepositCount int    `json:"depositCount"`
	OK           bool   `json:"ok"`
}

type Claim struct {
	At     string `json:"at"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Amount string `json:"amount"`
	Index  uint64 `json:"depositIndex"`
	TxHash string `json:"tx"`
	Block  uint64 `json:"block,omitempty"`
}

type bondMeta struct {
	kind  string
	label string
	addr  string
}

var bonds = []bondMeta{
	{kind: "fixed_price", label: "固定价销毁债券", addr: FixedPrice},
	{kind: "lgns_burn", label: "LGNS 销毁债券", addr: LgnsBurn},
}

// LoadViews fetches claimable / depositCount only (fast eth_call path).
func LoadViews(ctx context.Context, cli *ethclient.Client, addr string) Snapshot {
	out := Snapshot{
		FixedPrice: emptyView(bonds[0]),
		LgnsBurn:   emptyView(bonds[1]),
		Claims:     []Claim{},
	}
	if cli == nil || !common.IsHexAddress(addr) {
		out.Errors = append(out.Errors, "rpc unavailable")
		return out
	}
	addr = common.HexToAddress(addr).Hex()

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, b := range bonds {
		b := b
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := loadBond(ctx, cli, b, addr)
			mu.Lock()
			defer mu.Unlock()
			if b.kind == "fixed_price" {
				out.FixedPrice = v
			} else {
				out.LgnsBurn = v
			}
			if err != nil {
				out.Errors = append(out.Errors, b.kind+": "+err.Error())
			}
		}()
	}
	wg.Wait()
	return out
}

// ClampClaimLimit normalizes page size.
func ClampClaimLimit(limit int) int {
	if limit <= 0 {
		return defaultClaimLimit
	}
	if limit > maxClaimLimit {
		return maxClaimLimit
	}
	return limit
}

// LoadClaims loads fixed-price payout history.
// Homepage (forceRPC=false, beforeBlock=0): prefer count API; fallback to LGNS Transfer logs.
// More pages (forceRPC=true): LGNS Transfer from PayoutFixed only.
func LoadClaims(ctx context.Context, cli *ethclient.Client, addr string, limit int, beforeBlock uint64, forceRPC bool) ClaimsPage {
	limit = ClampClaimLimit(limit)
	page := ClaimsPage{Claims: []Claim{}, Source: "rpc"}
	if !common.IsHexAddress(addr) {
		page.Errors = append(page.Errors, "invalid address")
		return page
	}
	addr = common.HexToAddress(addr).Hex()

	if !forceRPC && beforeBlock == 0 {
		claims, err := FetchClaimsFromCount(ctx, addr)
		if err == nil && len(claims) > 0 {
			if len(claims) > limit {
				claims = claims[:limit]
			}
			page.Claims = claims
			page.Source = "count"
			// Allow UI "show more" via RPC even when count returned a short window.
			page.NextBefore = 0
			return page
		}
		if err != nil {
			page.Errors = append(page.Errors, "count: "+err.Error())
		}
	}

	if cli == nil {
		page.Errors = append(page.Errors, "rpc unavailable")
		return page
	}
	claims, next, err := LoadClaimsViaTransfer(ctx, cli, addr, limit, beforeBlock)
	if err != nil {
		page.Errors = append(page.Errors, "rpc: "+err.Error())
	}
	if claims == nil {
		claims = []Claim{}
	}
	page.Claims = claims
	page.NextBefore = next
	page.Source = "rpc"
	return page
}

// Load fetches claimable balances and recent claims for addr.
func Load(ctx context.Context, cli *ethclient.Client, addr string) Snapshot {
	out := LoadViews(ctx, cli, addr)
	page := LoadClaims(ctx, cli, addr, defaultClaimLimit, 0, false)
	out.Claims = page.Claims
	out.Errors = append(out.Errors, page.Errors...)
	return out
}

// SumClaimedLGNS sums payout amounts from a bond claim receipt.
// Prefers LGNS Transfer from PayoutFixed; falls back to bond Claimed events.
func SumClaimedLGNS(rec *types.Receipt) (formatted string, raw *big.Int, n int) {
	raw = big.NewInt(0)
	if rec == nil {
		return "", raw, 0
	}
	fixed := common.HexToAddress(FixedPrice)
	lgnsBurn := common.HexToAddress(LgnsBurn)
	lgnsTok := common.HexToAddress(LGNSToken)
	payout := common.HexToAddress(PayoutFixed)
	payoutTopic := common.BytesToHash(common.LeftPadBytes(payout.Bytes(), 32))
	for _, lg := range rec.Logs {
		if lg == nil || len(lg.Topics) < 1 {
			continue
		}
		if lg.Address == lgnsTok && lg.Topics[0] == transferTopic && len(lg.Topics) >= 3 && lg.Topics[1] == payoutTopic {
			amount := big.NewInt(0)
			if len(lg.Data) >= 32 {
				amount = new(big.Int).SetBytes(lg.Data[len(lg.Data)-32:])
			}
			raw.Add(raw, amount)
			n++
			continue
		}
		if lg.Topics[0] != claimedTopic {
			continue
		}
		if lg.Address != fixed && lg.Address != lgnsBurn {
			continue
		}
		amount := big.NewInt(0)
		if len(lg.Data) >= 32 {
			amount = new(big.Int).SetBytes(lg.Data[len(lg.Data)-32:])
		}
		raw.Add(raw, amount)
		n++
	}
	if n == 0 || raw.Sign() <= 0 {
		return "", raw, n
	}
	return tokenbal.Format(raw, lgnsDecimals), raw, n
}

// IsBondClaimContract reports whether to is a fixed-price or LGNS burn bond.
func IsBondClaimContract(to string) bool {
	if to == "" {
		return false
	}
	a := common.HexToAddress(to)
	return a == common.HexToAddress(FixedPrice) || a == common.HexToAddress(LgnsBurn)
}

func emptyView(b bondMeta) BondView {
	return BondView{
		Label:     b.label,
		Contract:  b.addr,
		Claimable: "—",
	}
}

func loadBond(ctx context.Context, cli *ethclient.Client, b bondMeta, user string) (BondView, error) {
	v := emptyView(b)
	raw, err := callUint(ctx, cli, b.addr, selTotalClaimable, padAddr(user))
	if err != nil {
		return v, err
	}
	cnt, err := callUint(ctx, cli, b.addr, selDepositCount, padAddr(user))
	if err != nil {
		return v, err
	}
	v.ClaimableRaw = raw.String()
	v.Claimable = tokenbal.Format(raw, lgnsDecimals)
	v.DepositCount = int(cnt.Uint64())
	v.OK = true
	return v, nil
}

func padAddr(a string) []byte {
	return common.LeftPadBytes(common.HexToAddress(a).Bytes(), 32)
}

func callUint(ctx context.Context, cli *ethclient.Client, to, selector string, args ...[]byte) (*big.Int, error) {
	data := common.Hex2Bytes(selector)
	for _, a := range args {
		data = append(data, a...)
	}
	out, err := helixchain.Call(ctx, cli, to, data)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(out), nil
}

// LoadClaimsViaTransfer scans LGNS Transfer logs from PayoutFixed to user.
// Returns nextBeforeBlock (minBlock-1) when a full page was filled; else 0.
func LoadClaimsViaTransfer(ctx context.Context, cli *ethclient.Client, user string, limit int, beforeBlock uint64) ([]Claim, uint64, error) {
	limit = ClampClaimLimit(limit)
	latest, err := cli.BlockNumber(ctx)
	if err != nil {
		return []Claim{}, 0, err
	}
	end := latest
	if beforeBlock > 0 && beforeBlock < end {
		end = beforeBlock
	}
	from := uint64(0)
	if end > transferLookback {
		from = end - transferLookback
	}

	token := common.HexToAddress(LGNSToken)
	fromTopic := common.BytesToHash(common.LeftPadBytes(common.HexToAddress(PayoutFixed).Bytes(), 32))
	toTopic := common.BytesToHash(common.LeftPadBytes(common.HexToAddress(user).Bytes(), 32))

	var all []Claim
	var chunkErrs []string
	okChunks := 0
	blockTs := map[uint64]time.Time{}

	for {
		var start uint64
		if end+1 > maxLogRange {
			start = end - maxLogRange + 1
		}
		if start < from {
			start = from
		}
		q := ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(start),
			ToBlock:   new(big.Int).SetUint64(end),
			Addresses: []common.Address{token},
			Topics:    [][]common.Hash{{transferTopic}, {fromTopic}, {toTopic}},
		}
		logs, err := cli.FilterLogs(ctx, q)
		if err != nil {
			chunkErrs = append(chunkErrs, fmt.Sprintf("xfer@%d-%d: %v", start, end, err))
		} else {
			okChunks++
			// Process newest first within the chunk.
			for i := len(logs) - 1; i >= 0; i-- {
				lg := logs[i]
				amount := big.NewInt(0)
				if len(lg.Data) >= 32 {
					amount = new(big.Int).SetBytes(lg.Data[len(lg.Data)-32:])
				}
				at := blockTs[lg.BlockNumber]
				if at.IsZero() {
					hdr, herr := cli.HeaderByNumber(ctx, new(big.Int).SetUint64(lg.BlockNumber))
					if herr == nil && hdr != nil {
						at = time.Unix(int64(hdr.Time), 0).UTC()
						blockTs[lg.BlockNumber] = at
					} else {
						at = time.Now().UTC()
					}
				}
				all = append(all, Claim{
					At:     at.Format(time.RFC3339),
					Kind:   "fixed_price",
					Label:  "固定价销毁债券",
					Amount: tokenbal.Format(amount, lgnsDecimals),
					TxHash: lg.TxHash.Hex(),
					Block:  lg.BlockNumber,
				})
				if len(all) >= limit {
					break
				}
			}
		}
		if len(all) >= limit || start <= from {
			break
		}
		end = start - 1
		if ctx.Err() != nil {
			break
		}
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Block != all[j].Block {
			return all[i].Block > all[j].Block
		}
		return strings.ToLower(all[i].TxHash) > strings.ToLower(all[j].TxHash)
	})
	if len(all) > limit {
		all = all[:limit]
	}

	nextBefore := uint64(0)
	if len(all) >= limit {
		minBlock := all[len(all)-1].Block
		if minBlock > 0 {
			nextBefore = minBlock - 1
		}
	}

	if okChunks == 0 && len(chunkErrs) > 0 {
		return all, nextBefore, fmt.Errorf("%s", strings.Join(chunkErrs, "; "))
	}
	return all, nextBefore, nil
}
