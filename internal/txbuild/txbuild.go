package txbuild

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
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
	fn := EffectiveFunctionName(preset, params)
	selHex := strings.TrimPrefix(preset.Selector, "0x")
	switch fn {
	case "claimAll":
		selHex = "d1058e59"
	case "claim":
		selHex = "379607f5"
	}
	sel, err := hex.DecodeString(selHex)
	if err != nil || len(sel) != 4 {
		return nil, fmt.Errorf("bad selector")
	}
	switch fn {
	case "claimAll":
		return append([]byte{}, sel...), nil
	case "claim":
		var arg int64
		if len(params.Args) > 0 {
			n, err := strconv.ParseInt(strings.TrimSpace(params.Args[0]), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bad claim arg: %w", err)
			}
			arg = n
		} else if params.EffectiveClaimMode() == "index" {
			arg = params.ClaimArg
		} else {
			arg = params.ClaimArg
			if arg == 0 {
				arg = preset.DefaultParams.ClaimArg
			}
			if arg == 0 {
				arg = 1
			}
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
		return nil, fmt.Errorf("unknown method %q for task type %s (provide abi)", fn, taskType)
	}
}

func BuildLegacyTx(to common.Address, data []byte, nonce uint64, gasLimit uint64, gasPrice *big.Int, chainID *big.Int) *types.Transaction {
	return types.NewTransaction(nonce, to, big.NewInt(0), gasLimit, gasPrice, data)
}
