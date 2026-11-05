package rpc

import "github.com/ethereum/go-ethereum/common"

func mustAddr(s string) common.Address {
	return common.HexToAddress(s)
}
