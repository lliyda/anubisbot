package tokenbal

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	decMu    sync.Mutex
	decCache = map[string]uint8{}
)

func BalanceOf(ctx context.Context, cli *ethclient.Client, token, holder string) (*big.Int, error) {
	to := common.HexToAddress(token)
	data := common.Hex2Bytes("70a08231")
	data = append(data, common.LeftPadBytes(common.HexToAddress(holder).Bytes(), 32)...)
	out, err := cli.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(out), nil
}

func Allowance(ctx context.Context, cli *ethclient.Client, token, owner, spender string) (*big.Int, error) {
	to := common.HexToAddress(token)
	data := common.Hex2Bytes("dd62ed3e")
	data = append(data, common.LeftPadBytes(common.HexToAddress(owner).Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(common.HexToAddress(spender).Bytes(), 32)...)
	out, err := cli.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(out), nil
}

// ApproveCalldata builds ERC20 approve(spender, amount) calldata.
func ApproveCalldata(spender string, amount *big.Int) []byte {
	if amount == nil {
		amount = big.NewInt(0)
	}
	data := common.Hex2Bytes("095ea7b3")
	data = append(data, common.LeftPadBytes(common.HexToAddress(spender).Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
	return data
}

func MaxUint256() *big.Int {
	n := new(big.Int).Lsh(big.NewInt(1), 256)
	return n.Sub(n, big.NewInt(1))
}

func Decimals(ctx context.Context, cli *ethclient.Client, token string, fallback uint8) uint8 {
	key := strings.ToLower(token)
	decMu.Lock()
	if d, ok := decCache[key]; ok {
		decMu.Unlock()
		return d
	}
	decMu.Unlock()

	to := common.HexToAddress(token)
	data := common.Hex2Bytes("313ce567")
	out, err := cli.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil || len(out) == 0 {
		return fallback
	}
	d := uint8(new(big.Int).SetBytes(out).Uint64())
	decMu.Lock()
	decCache[key] = d
	decMu.Unlock()
	return d
}

func Format(amount *big.Int, decimals uint8) string {
	if amount == nil {
		return "0"
	}
	if decimals == 0 {
		return amount.String()
	}
	neg := amount.Sign() < 0
	a := new(big.Int).Abs(amount)
	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	intPart := new(big.Int).Div(a, base)
	frac := new(big.Int).Mod(a, base)
	fracStr := fmt.Sprintf("%0*s", decimals, frac.String())
	// trim to 6 decimals max then strip trailing zeros
	if len(fracStr) > 6 {
		fracStr = fracStr[:6]
	}
	fracStr = strings.TrimRight(fracStr, "0")
	out := intPart.String()
	if fracStr != "" {
		out += "." + fracStr
	}
	if neg {
		out = "-" + out
	}
	return out
}
