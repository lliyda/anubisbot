package rpc

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

type mockDataErr struct {
	msg  string
	data interface{}
}

func (e mockDataErr) Error() string          { return e.msg }
func (e mockDataErr) ErrorData() interface{} { return e.data }

func TestDecodeRevertErrorFromDataError(t *testing.T) {
	// Raw output from Anubis explorer raw_trace for BurnBond: claim cap
	raw := "0x08c379a0000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000134275726e426f6e643a20636c61696d2063617000000000000000000000000000"
	got := DecodeRevertError(mockDataErr{
		msg:  "execution reverted",
		data: raw,
	})
	want := "BurnBond: claim cap"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDecodeRevertErrorFromPlainMessage(t *testing.T) {
	got := DecodeRevertError(errors.New("execution reverted: BurnBond: claim cap"))
	if got != "BurnBond: claim cap" {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeRevertDataFromBytes(t *testing.T) {
	raw := common.FromHex("0x08c379a0000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000134275726e426f6e643a20636c61696d2063617000000000000000000000000000")
	got := decodeRevertData(raw)
	if got != "BurnBond: claim cap" {
		t.Fatalf("got %q", got)
	}
	if _, err := abi.UnpackRevert(raw); err != nil {
		t.Fatal(err)
	}
}

func TestFormatRevertedOnChain(t *testing.T) {
	if got := FormatRevertedOnChain(""); got != "tx reverted on-chain" {
		t.Fatalf("empty: %q", got)
	}
	want := "tx reverted on-chain · reason : BurnBond: claim cap"
	if got := FormatRevertedOnChain("BurnBond: claim cap"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
