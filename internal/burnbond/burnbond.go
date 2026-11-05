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
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	FixedPrice = "0x3f403751975e1EB06d95901f7FF246513444E71F"
	LgnsBurn   = "0x11b10C9827c5B7071E96fcAa143B4e6E86b17c69"

	selTotalClaimable = "267e8ab6"
	selDepositCount   = "1bae91a4"

	lgnsDecimals = 9
	defaultLimit = 50
	lookback     = 500_000 // blocks
	maxLogRange  = 5_000   // rpc.anubispace.org eth_getLogs cap
)

var claimedTopic = crypto.Keccak256Hash([]byte("Claimed(address,uint256,uint256)"))

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

// LoadClaims scans Claimed events (slow getLogs path).
func LoadClaims(ctx context.Context, cli *ethclient.Client, addr string, limit int) ([]Claim, []string) {
	if cli == nil || !common.IsHexAddress(addr) {
		return nil, []string{"rpc unavailable"}
	}
	addr = common.HexToAddress(addr).Hex()
	claims, err := loadClaims(ctx, cli, addr, limit)
	if err != nil {
		return claims, []string{"claims: " + err.Error()}
	}
	return claims, nil
}

// Load fetches claimable balances and recent Claimed events for addr.
func Load(ctx context.Context, cli *ethclient.Client, addr string) Snapshot {
	out := LoadViews(ctx, cli, addr)
	claims, errs := LoadClaims(ctx, cli, addr, defaultLimit)
	out.Claims = claims
	out.Errors = append(out.Errors, errs...)
	return out
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

func loadClaims(ctx context.Context, cli *ethclient.Client, user string, limit int) ([]Claim, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	latest, err := cli.BlockNumber(ctx)
	if err != nil {
		return nil, err
	}
	from := uint64(0)
	if latest > lookback {
		from = latest - lookback
	}
	userAddr := common.HexToAddress(user)
	userTopic := common.BytesToHash(common.LeftPadBytes(userAddr.Bytes(), 32))

	var all []Claim
	var chunkErrs []string
	okChunks := 0
	blockTs := map[uint64]time.Time{}

	for _, b := range bonds {
		addr := common.HexToAddress(b.addr)
		for end := latest; ; {
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
				Addresses: []common.Address{addr},
				Topics:    [][]common.Hash{{claimedTopic}, {userTopic}},
			}
			logs, err := cli.FilterLogs(ctx, q)
			if err != nil {
				chunkErrs = append(chunkErrs, fmt.Sprintf("%s@%d-%d: %v", b.kind, start, end, err))
			} else {
				okChunks++
				for _, lg := range logs {
					amount := big.NewInt(0)
					if len(lg.Data) >= 32 {
						amount = new(big.Int).SetBytes(lg.Data[len(lg.Data)-32:])
					}
					idx := uint64(0)
					if len(lg.Topics) >= 3 {
						idx = new(big.Int).SetBytes(lg.Topics[2].Bytes()).Uint64()
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
						Kind:   b.kind,
						Label:  b.label,
						Amount: tokenbal.Format(amount, lgnsDecimals),
						Index:  idx,
						TxHash: lg.TxHash.Hex(),
						Block:  lg.BlockNumber,
					})
				}
			}
			if start <= from {
				break
			}
			end = start - 1
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
	// Only hard-fail when every chunk failed (avoids burnBonds banner on partial RPC blips).
	if okChunks == 0 && len(chunkErrs) > 0 {
		return all, fmt.Errorf("%s", strings.Join(chunkErrs, "; "))
	}
	return all, nil
}
