package batch

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"anubisbot/internal/config"
	"anubisbot/internal/logstore"
	"anubisbot/internal/rpc"
	"anubisbot/internal/tasks"
	"anubisbot/internal/tokenbal"
	"anubisbot/internal/txbuild"
	"anubisbot/internal/wallets"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type Runner struct {
	Mode          string
	Concurrency   int
	RPC           *rpc.Manager
	Wallets       *wallets.Store
	Tasks         *tasks.Store
	Presets       map[string]config.Preset
	Logs          *logstore.Store
	GasMultiplier float64
}

type FireResult struct {
	TaskID string
	TxHash string
	Error  string
	Sim    bool
	RPCID  string
}

type SignedTx struct {
	TaskID string
	FireAt time.Time
	Tx     *types.Transaction
	Sim    bool
	Data   []byte
	To     string
	Gas    uint64
	Price  *big.Int
}

type SimulateResult struct {
	OK     bool   `json:"ok"`
	TaskID string `json:"taskId"`
	To     string `json:"to"`
	From   string `json:"from"`
	Data   string `json:"data"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (r *Runner) Fire(ctx context.Context, list []tasks.Task) []FireResult {
	return r.FireSigned(ctx, list, nil)
}

func (r *Runner) FireSigned(ctx context.Context, list []tasks.Task, cache map[string]*SignedTx) []FireResult {
	if r.Concurrency <= 0 {
		r.Concurrency = 8
	}
	ids := r.RPC.ActiveIDs()
	leads := r.RPC.Leads()
	rtts := r.RPC.RTTs()
	var out []FireResult
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, r.Concurrency)

	for _, t := range list {
		t := t
		var cached *SignedTx
		if cache != nil {
			cached = cache[t.ID]
		}
		for _, rpcID := range ids {
			rpcID := rpcID
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				res := r.SendToRPC(ctx, t, cached, rpcID, rtts[rpcID], leads[rpcID])
				mu.Lock()
				out = append(out, res)
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	return out
}

func (r *Runner) Sign(ctx context.Context, t tasks.Task, fireAt time.Time) (*SignedTx, error) {
	preset, ok := r.Presets[t.TaskType]
	if !ok {
		return nil, fmt.Errorf("unknown preset")
	}
	data, err := txbuild.BuildCalldata(t.TaskType, preset, t.Params)
	if err != nil {
		return nil, err
	}
	gasLimit, gasPrice, err := r.gasOpts(ctx, t, preset)
	if err != nil {
		return nil, err
	}

	if r.Mode != "live" {
		return &SignedTx{
			TaskID: t.ID, FireAt: fireAt, Sim: true,
			Data: data, To: preset.To, Gas: gasLimit, Price: gasPrice,
		}, nil
	}

	key, err := r.Wallets.PrivateKey(t.WalletID)
	if err != nil {
		return nil, err
	}
	from := crypto.PubkeyToAddress(key.PublicKey)
	nonce, err := r.RPC.PendingNonce(ctx, from.Hex())
	if err != nil {
		return nil, err
	}
	to := common.HexToAddress(preset.To)
	tx := txbuild.BuildLegacyTx(to, data, nonce, gasLimit, gasPrice, r.RPC.ChainID())
	signer := types.NewEIP155Signer(r.RPC.ChainID())
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		return nil, err
	}
	return &SignedTx{
		TaskID: t.ID, FireAt: fireAt, Tx: signed,
		Data: data, To: preset.To, Gas: gasLimit, Price: gasPrice,
	}, nil
}

// SendToRPC broadcasts (or would_send) one task to one RPC with given lead metadata.
func (r *Runner) SendToRPC(ctx context.Context, t tasks.Task, cached *SignedTx, rpcID string, rtt, lead int64) FireResult {
	preset, ok := r.Presets[t.TaskType]
	if !ok {
		return r.failSoft(t, rpcID, rtt, "unknown preset")
	}
	data := cachedData(cached)
	gasLimit := uint64(0)
	var gasPrice *big.Int
	if cached != nil {
		gasLimit = cached.Gas
		gasPrice = cached.Price
	}
	if len(data) == 0 {
		var err error
		data, err = txbuild.BuildCalldata(t.TaskType, preset, t.Params)
		if err != nil {
			return r.failSoft(t, rpcID, rtt, err.Error())
		}
	}
	if gasLimit == 0 || gasPrice == nil {
		gl, gp, err := r.gasOpts(ctx, t, preset)
		if err != nil {
			return r.failSoft(t, rpcID, rtt, err.Error())
		}
		gasLimit, gasPrice = gl, gp
	}

	amt := AmountLabel(t, preset)
	if r.Mode != "live" {
		r.Logs.Add(logstore.Entry{
			TaskID: t.ID, Wallet: t.WalletName, Event: "broadcast",
			Message: fmt.Sprintf("广播 · %s · %s", rpcID, amt),
			RPCID: rpcID, RTTMs: rtt,
			Extra: map[string]any{"amount": amt, "sim": true, "rpcId": rpcID},
		})
		return FireResult{TaskID: t.ID, TxHash: "sim-" + t.ID + "-" + rpcID, Sim: true, RPCID: rpcID}
	}

	var signed *types.Transaction
	if cached != nil && cached.Tx != nil {
		signed = cached.Tx
	} else {
		key, err := r.Wallets.PrivateKey(t.WalletID)
		if err != nil {
			return r.failSoft(t, rpcID, rtt, err.Error())
		}
		from := crypto.PubkeyToAddress(key.PublicKey)
		nonce, err := r.RPC.PendingNonce(ctx, from.Hex())
		if err != nil {
			return r.failSoft(t, rpcID, rtt, err.Error())
		}
		to := common.HexToAddress(preset.To)
		tx := txbuild.BuildLegacyTx(to, data, nonce, gasLimit, gasPrice, r.RPC.ChainID())
		signer := types.NewEIP155Signer(r.RPC.ChainID())
		signed, err = types.SignTx(tx, signer, key)
		if err != nil {
			return r.failSoft(t, rpcID, rtt, err.Error())
		}
	}

	err := r.RPC.SendTransaction(ctx, rpcID, signed)
	if err != nil {
		err2 := r.RPC.SendTransaction(ctx, rpcID, signed)
		if err2 != nil {
			return r.failSoft(t, rpcID, rtt, err2.Error())
		}
	}
	hash := signed.Hash().Hex()
	r.Logs.Add(logstore.Entry{
		TaskID: t.ID, Wallet: t.WalletName, Event: "broadcast",
		Message: fmt.Sprintf("广播 · %s · %s", rpcID, amt),
		RPCID: rpcID, RTTMs: rtt, TxHash: hash,
		Extra: map[string]any{"amount": amt, "rpcId": rpcID},
	})
	return FireResult{TaskID: t.ID, TxHash: hash, RPCID: rpcID}
}

// AmountLabel returns human-readable claim/burn amount for logs.
func AmountLabel(t tasks.Task, preset config.Preset) string {
	name := strings.TrimSpace(preset.Function)
	if name == "" {
		name = strings.TrimSpace(preset.Method)
	}
	args := t.Params.Args
	if len(args) == 0 {
		args = preset.DefaultParams.Args
	}
	switch name {
	case "burnForPower":
		raw := ""
		if len(args) > 0 {
			raw = args[0]
		}
		if raw == "" {
			raw = t.Params.BurnAmount
		}
		if raw == "" {
			raw = preset.DefaultParams.BurnAmount
		}
		if raw == "" {
			return "burn —"
		}
		n := new(big.Int)
		if _, ok := n.SetString(raw, 10); !ok {
			return "burn " + raw
		}
		// LGNS 9 decimals
		base := new(big.Int).Exp(big.NewInt(10), big.NewInt(9), nil)
		whole := new(big.Int).Div(n, base)
		return whole.String() + " LGNS"
	case "claimAll":
		return "claimAll"
	case "claim":
		raw := ""
		if len(args) > 0 {
			raw = args[0]
		}
		if raw != "" {
			return "claim " + raw
		}
		arg := t.Params.ClaimArg
		if arg == 0 {
			arg = preset.DefaultParams.ClaimArg
		}
		if arg == 0 {
			arg = 1
		}
		return fmt.Sprintf("claim %d", arg)
	default:
		if name == "" {
			name = "call"
		}
		if len(args) == 0 {
			return name
		}
		return name + "(" + strings.Join(args, ", ") + ")"
	}
}

func cachedData(c *SignedTx) []byte {
	if c == nil {
		return nil
	}
	return c.Data
}

type ApproveResult struct {
	OK     bool   `json:"ok"`
	Sim    bool   `json:"sim,omitempty"`
	TxHash string `json:"txHash,omitempty"`
	To     string `json:"to"`
	Spender string `json:"spender"`
	Error  string `json:"error,omitempty"`
}

// ApproveLGNS sends LGNS.approve(spender, amount). amount nil → maxUint256.
// In non-live mode only logs would_approve.
func (r *Runner) ApproveLGNS(ctx context.Context, t tasks.Task, token, spender string, amount *big.Int) (ApproveResult, error) {
	if amount == nil {
		amount = tokenbal.MaxUint256()
	}
	data := tokenbal.ApproveCalldata(spender, amount)
	out := ApproveResult{To: token, Spender: spender}
	amtLabel := amount.String()
	if amount.Cmp(tokenbal.MaxUint256()) == 0 {
		amtLabel = "maxUint256"
	}
	if r.Mode != "live" {
		r.Logs.Add(logstore.Entry{
			TaskID: t.ID, Wallet: t.WalletName, Event: "would_approve",
			Message: fmt.Sprintf("would_approve LGNS spender=%s amount=%s wallet=%s", spender, amtLabel, t.WalletAddress),
		})
		out.OK = true
		out.Sim = true
		out.TxHash = "sim-approve-" + t.ID
		return out, nil
	}
	key, err := r.Wallets.PrivateKey(t.WalletID)
	if err != nil {
		return out, err
	}
	from := crypto.PubkeyToAddress(key.PublicKey)
	nonce, err := r.RPC.PendingNonce(ctx, from.Hex())
	if err != nil {
		return out, err
	}
	gp, err := r.RPC.SuggestGasPrice(ctx)
	if err != nil || gp == nil {
		gp = big.NewInt(3_000_000_000)
	}
	mult := r.GasMultiplier
	if mult <= 0 {
		mult = 1.2
	}
	gasPrice := new(big.Int).Mul(gp, big.NewInt(int64(mult*100)))
	gasPrice.Div(gasPrice, big.NewInt(100))
	to := common.HexToAddress(token)
	tx := txbuild.BuildLegacyTx(to, data, nonce, 80000, gasPrice, r.RPC.ChainID())
	signer := types.NewEIP155Signer(r.RPC.ChainID())
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		return out, err
	}
	ids := r.RPC.ActiveIDs()
	if len(ids) == 0 {
		return out, fmt.Errorf("no active rpc")
	}
	if err := r.RPC.SendTransaction(ctx, ids[0], signed); err != nil {
		return out, err
	}
	hash := signed.Hash().Hex()
	r.Logs.Add(logstore.Entry{
		TaskID: t.ID, Wallet: t.WalletName, Event: "approve_sent",
		Message: fmt.Sprintf("LGNS approve spender=%s amount=%s", spender, amtLabel),
		TxHash: hash, RPCID: ids[0],
	})
	out.OK = true
	out.TxHash = hash
	return out, nil
}

func (r *Runner) Simulate(ctx context.Context, t tasks.Task, fromOverride string) SimulateResult {
	preset, ok := r.Presets[t.TaskType]
	if !ok {
		return SimulateResult{OK: false, TaskID: t.ID, Error: "unknown preset"}
	}
	from := strings.TrimSpace(fromOverride)
	if from == "" {
		from = t.WalletAddress
	}
	res := r.SimulatePreset(ctx, t.TaskType, preset, t.Params, from)
	if res.TaskID == t.TaskType {
		res.TaskID = t.ID
	}
	return res
}

// SimulatePreset runs eth_call using a task type preset and explicit from address.
func (r *Runner) SimulatePreset(ctx context.Context, taskType string, preset config.Preset, params tasks.TaskParams, fromAddr string) SimulateResult {
	fromAddr = strings.TrimSpace(fromAddr)
	if !common.IsHexAddress(fromAddr) {
		return SimulateResult{OK: false, TaskID: taskType, Error: "invalid from address"}
	}
	data, err := txbuild.BuildCalldata(taskType, preset, params)
	if err != nil {
		return SimulateResult{OK: false, TaskID: taskType, Error: err.Error()}
	}
	from := common.HexToAddress(fromAddr)
	to := common.HexToAddress(preset.To)
	cli, err := r.RPC.Client()
	if err != nil {
		return SimulateResult{OK: false, TaskID: taskType, To: preset.To, From: from.Hex(), Data: "0x" + common.Bytes2Hex(data), Error: err.Error()}
	}
	msg := ethereum.CallMsg{From: from, To: &to, Data: data}
	out, err := cli.CallContract(ctx, msg, nil)
	walletLabel := from.Hex()
	if len(walletLabel) > 10 {
		walletLabel = walletLabel[:6] + "…" + walletLabel[len(walletLabel)-4:]
	}
	res := SimulateResult{
		TaskID: taskType,
		To:     preset.To,
		From:   from.Hex(),
		Data:   "0x" + common.Bytes2Hex(data),
	}
	if err != nil {
		res.OK = false
		res.Error = decodeCallErr(err)
		r.Logs.Add(logstore.Entry{TaskID: taskType, Wallet: walletLabel, Event: "simulate_fail", Message: res.Error})
		return res
	}
	res.OK = true
	res.Result = "0x" + common.Bytes2Hex(out)
	r.Logs.Add(logstore.Entry{TaskID: taskType, Wallet: walletLabel, Event: "simulate_ok", Message: "eth_call ok"})
	return res
}

func decodeCallErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.Index(msg, "execution reverted"); i >= 0 {
		return msg[i:]
	}
	return msg
}

func (r *Runner) gasOpts(ctx context.Context, t tasks.Task, preset config.Preset) (uint64, *big.Int, error) {
	gasLimit := t.Params.GasLimit
	if gasLimit == 0 {
		gasLimit = preset.DefaultParams.GasLimit
	}
	if gasLimit == 0 {
		gasLimit = 300000
	}
	var gasPrice *big.Int
	if t.Params.GasPriceGwei != nil {
		gasPrice = new(big.Int).SetInt64(int64(*t.Params.GasPriceGwei * 1e9))
	} else {
		gp, err := r.RPC.SuggestGasPrice(ctx)
		if err != nil {
			gp = big.NewInt(3_000_000_000)
		}
		mult := r.GasMultiplier
		if mult <= 0 {
			mult = 1.2
		}
		gasPrice = new(big.Int).Mul(gp, big.NewInt(int64(mult*100)))
		gasPrice.Div(gasPrice, big.NewInt(100))
	}
	return gasLimit, gasPrice, nil
}

func (r *Runner) failSoft(t tasks.Task, rpcID string, rtt int64, msg string) FireResult {
	r.Logs.Add(logstore.Entry{
		TaskID: t.ID, Wallet: t.WalletName, Event: "failed", Error: msg, Message: msg,
		RPCID: rpcID, RTTMs: rtt,
	})
	return FireResult{TaskID: t.ID, Error: msg, RPCID: rpcID}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
