package helixchain

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func Call(ctx context.Context, cli *ethclient.Client, to string, data []byte) ([]byte, error) {
	addr := common.HexToAddress(to)
	return cli.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: data}, nil)
}

func sel(hex4 string) []byte {
	return common.Hex2Bytes(hex4)
}

func padAddr(a string) []byte {
	return common.LeftPadBytes(common.HexToAddress(a).Bytes(), 32)
}

func uint256(out []byte) *big.Int {
	if len(out) == 0 {
		return big.NewInt(0)
	}
	return new(big.Int).SetBytes(out)
}

func callUint(ctx context.Context, cli *ethclient.Client, to, selector string, args ...[]byte) (*big.Int, error) {
	data := sel(selector)
	for _, a := range args {
		data = append(data, a...)
	}
	out, err := Call(ctx, cli, to, data)
	if err != nil {
		return nil, err
	}
	return uint256(out), nil
}
