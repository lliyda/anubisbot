package txbuild

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"anubisbot/internal/config"
	"anubisbot/internal/tasks"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func BuildCalldata(taskType string, preset config.Preset, params tasks.TaskParams) ([]byte, error) {
	if len(bytes.TrimSpace(preset.ABI)) > 0 && string(bytes.TrimSpace(preset.ABI)) != "null" {
		data, err := PackCalldata(preset, params)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", taskType, err)
		}
		return data, nil
	}
	// legacy fallback: method + selector
	sel, err := hex.DecodeString(strings.TrimPrefix(preset.Selector, "0x"))
	if err != nil || len(sel) != 4 {
		return nil, fmt.Errorf("bad selector")
	}
	switch strings.TrimSpace(preset.Method) {
	case "claimAll":
		return append([]byte{}, sel...), nil
	case "claim":
		arg := params.ClaimArg
		if arg == 0 {
			arg = preset.DefaultParams.ClaimArg
		}
		if arg == 0 {
			arg = 1
		}
		data := append([]byte{}, sel...)
		data = append(data, common.LeftPadBytes(big.NewInt(arg).Bytes(), 32)...)
		return data, nil
	case "burnForPower":
		amt := params.BurnAmount
		if amt == "" {
			amt = preset.DefaultParams.BurnAmount
		}
		if amt == "" {
			amt = "500000000000"
		}
		n := new(big.Int)
		if _, ok := n.SetString(amt, 10); !ok {
			return nil, fmt.Errorf("bad burnAmount")
		}
		data := append([]byte{}, sel...)
		data = append(data, common.LeftPadBytes(n.Bytes(), 32)...)
		return data, nil
	default:
		return nil, fmt.Errorf("unknown method %q for task type %s (provide abi)", preset.Method, taskType)
	}
}

func BuildLegacyTx(to common.Address, data []byte, nonce uint64, gasLimit uint64, gasPrice *big.Int, chainID *big.Int) *types.Transaction {
	return types.NewTransaction(nonce, to, big.NewInt(0), gasLimit, gasPrice, data)
}
