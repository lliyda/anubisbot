package rpc

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// DecodeRevertError extracts a human-readable revert reason from an eth_call / RPC error.
// Handles Error(string) ABI data via rpc.DataError, and plain "execution reverted: …" messages.
func DecodeRevertError(err error) string {
	if err == nil {
		return ""
	}
	if data, ok := extractErrorData(err); ok {
		if reason := decodeRevertData(data); reason != "" {
			return reason
		}
	}
	return stripExecutionReverted(err.Error())
}

func extractErrorData(err error) ([]byte, bool) {
	type dataErr interface {
		ErrorData() interface{}
	}
	de, ok := err.(dataErr)
	if !ok {
		return nil, false
	}
	switch v := de.ErrorData().(type) {
	case string:
		return decodeHexBytes(v)
	case []byte:
		if len(v) == 0 {
			return nil, false
		}
		return v, true
	default:
		return nil, false
	}
}

func decodeHexBytes(s string) ([]byte, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	b, err := hex.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil, false
	}
	return b, true
}

func decodeRevertData(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if reason, err := abi.UnpackRevert(data); err == nil {
		return strings.TrimSpace(reason)
	}
	return ""
}

func stripExecutionReverted(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	lower := strings.ToLower(msg)
	const marker = "execution reverted"
	if i := strings.Index(lower, marker); i >= 0 {
		rest := strings.TrimSpace(msg[i+len(marker):])
		rest = strings.TrimPrefix(rest, ":")
		rest = strings.TrimSpace(rest)
		if rest != "" {
			return rest
		}
		return "execution reverted"
	}
	return msg
}

// FormatRevertedOnChain builds the LastError string for a mined but reverted tx.
func FormatRevertedOnChain(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "tx reverted on-chain"
	}
	return "tx reverted on-chain · reason : " + reason
}

// RevertReason replays the mined transaction via eth_call and returns the revert string, or "".
func (m *Manager) RevertReason(ctx context.Context, rpcID, txHash string, blockNumber *big.Int) string {
	if m == nil || strings.TrimSpace(txHash) == "" {
		return ""
	}
	cli, err := m.ClientByID(rpcID)
	if err != nil {
		cli, err = m.Client()
		if err != nil {
			return ""
		}
	}
	hash := common.HexToHash(txHash)
	tx, _, err := cli.TransactionByHash(ctx, hash)
	if err != nil || tx == nil {
		return ""
	}
	chainID := m.ChainID()
	if tx.ChainId() != nil && tx.ChainId().Sign() > 0 {
		chainID = tx.ChainId()
	}
	signer := types.LatestSignerForChainID(chainID)
	from, err := types.Sender(signer, tx)
	if err != nil {
		return ""
	}
	to := tx.To()
	msg := ethereum.CallMsg{
		From:  from,
		To:    to,
		Gas:   tx.Gas(),
		Value: tx.Value(),
		Data:  tx.Data(),
	}
	if tx.Type() >= types.DynamicFeeTxType {
		msg.GasFeeCap = tx.GasFeeCap()
		msg.GasTipCap = tx.GasTipCap()
	} else {
		msg.GasPrice = tx.GasPrice()
	}

	tryAt := func(bn *big.Int) string {
		_, callErr := cli.CallContract(ctx, msg, bn)
		if callErr == nil {
			return ""
		}
		return DecodeRevertError(callErr)
	}

	if blockNumber != nil && blockNumber.Sign() > 0 {
		if reason := tryAt(blockNumber); reason != "" {
			return reason
		}
		prev := new(big.Int).Sub(blockNumber, big.NewInt(1))
		if prev.Sign() >= 0 {
			if reason := tryAt(prev); reason != "" {
				return reason
			}
		}
	}
	return tryAt(nil)
}
