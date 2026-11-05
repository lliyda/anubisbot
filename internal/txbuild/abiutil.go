package txbuild

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"anubisbot/internal/config"
	"anubisbot/internal/tasks"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// ResolveMethod parses preset ABI and returns the target method.
func ResolveMethod(preset config.Preset) (abi.ABI, abi.Method, error) {
	raw := bytes.TrimSpace(preset.ABI)
	if len(raw) == 0 || string(raw) == "null" {
		return abi.ABI{}, abi.Method{}, fmt.Errorf("abi required")
	}
	parsed, err := abi.JSON(bytes.NewReader(raw))
	if err != nil {
		return abi.ABI{}, abi.Method{}, fmt.Errorf("invalid abi: %w", err)
	}
	name := strings.TrimSpace(preset.Function)
	if name == "" {
		name = strings.TrimSpace(preset.Method)
	}
	if name != "" {
		m, ok := parsed.Methods[name]
		if !ok {
			return abi.ABI{}, abi.Method{}, fmt.Errorf("function %q not found in abi", name)
		}
		return parsed, m, nil
	}
	if len(parsed.Methods) == 1 {
		for _, m := range parsed.Methods {
			return parsed, m, nil
		}
	}
	names := make([]string, 0, len(parsed.Methods))
	for n := range parsed.Methods {
		names = append(names, n)
	}
	return abi.ABI{}, abi.Method{}, fmt.Errorf("abi has multiple functions; set function (%s)", strings.Join(names, ", "))
}

// DeriveSelector returns 4-byte selector hex without 0x.
func DeriveSelector(m abi.Method) string {
	return hex.EncodeToString(m.ID)
}

func resolveArgStrings(preset config.Preset, params tasks.TaskParams) []string {
	if len(params.Args) > 0 {
		return params.Args
	}
	if len(preset.DefaultParams.Args) > 0 {
		out := make([]string, len(preset.DefaultParams.Args))
		copy(out, preset.DefaultParams.Args)
		return out
	}
	// legacy claim / burn fields
	name := strings.TrimSpace(preset.Function)
	if name == "" {
		name = strings.TrimSpace(preset.Method)
	}
	switch name {
	case "burnForPower":
		amt := params.BurnAmount
		if amt == "" {
			amt = preset.DefaultParams.BurnAmount
		}
		if amt == "" {
			amt = "500000000000"
		}
		return []string{amt}
	default:
		arg := params.ClaimArg
		if arg == 0 {
			arg = preset.DefaultParams.ClaimArg
		}
		if arg == 0 {
			arg = 1
		}
		return []string{strconv.FormatInt(arg, 10)}
	}
}

func parseABIArg(t abi.Type, raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	switch t.T {
	case abi.IntTy, abi.UintTy:
		if raw == "" {
			return nil, fmt.Errorf("empty integer")
		}
		n := new(big.Int)
		if _, ok := n.SetString(raw, 0); !ok {
			return nil, fmt.Errorf("bad integer %q", raw)
		}
		return n, nil
	case abi.AddressTy:
		if !common.IsHexAddress(raw) {
			return nil, fmt.Errorf("bad address %q", raw)
		}
		return common.HexToAddress(raw), nil
	case abi.BoolTy:
		switch strings.ToLower(raw) {
		case "1", "true", "yes":
			return true, nil
		case "0", "false", "no":
			return false, nil
		default:
			return nil, fmt.Errorf("bad bool %q", raw)
		}
	case abi.StringTy:
		return raw, nil
	case abi.BytesTy:
		b, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
		if err != nil {
			return nil, fmt.Errorf("bad bytes: %w", err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("unsupported abi type %s", t.String())
	}
}

// PackCalldata encodes function call from ABI + positional string args.
func PackCalldata(preset config.Preset, params tasks.TaskParams) ([]byte, error) {
	parsed, method, err := ResolveMethod(preset)
	if err != nil {
		return nil, err
	}
	rawArgs := resolveArgStrings(preset, params)
	if len(rawArgs) != len(method.Inputs) {
		return nil, fmt.Errorf("args: want %d got %d for %s", len(method.Inputs), len(rawArgs), method.Name)
	}
	vals := make([]any, len(method.Inputs))
	for i, in := range method.Inputs {
		v, err := parseABIArg(in.Type, rawArgs[i])
		if err != nil {
			name := in.Name
			if name == "" {
				name = fmt.Sprintf("#%d", i)
			}
			return nil, fmt.Errorf("arg %s: %w", name, err)
		}
		vals[i] = v
	}
	return parsed.Pack(method.Name, vals...)
}

// NormalizePresetFromABI fills Method/Selector from ABI; synthesizes ABI from legacy method if needed.
func NormalizePresetFromABI(p *config.Preset) error {
	raw := bytes.TrimSpace(p.ABI)
	if len(raw) == 0 || string(raw) == "null" {
		switch strings.TrimSpace(p.Method) {
		case "claim":
			p.ABI = json.RawMessage(`[{"type":"function","name":"claim","stateMutability":"nonpayable","inputs":[{"name":"arg","type":"uint256"}],"outputs":[]}]`)
		case "burnForPower":
			p.ABI = json.RawMessage(`[{"type":"function","name":"burnForPower","stateMutability":"nonpayable","inputs":[{"name":"amount","type":"uint256"}],"outputs":[]}]`)
		default:
			return fmt.Errorf("abi required")
		}
	}
	// compact / validate JSON array
	var arr []json.RawMessage
	if err := json.Unmarshal(p.ABI, &arr); err != nil {
		return fmt.Errorf("abi must be a JSON array: %w", err)
	}
	compact, err := json.Marshal(arr)
	if err != nil {
		return err
	}
	p.ABI = compact

	_, method, err := ResolveMethod(*p)
	if err != nil {
		return err
	}
	p.Function = method.Name
	p.Method = method.Name
	p.Selector = DeriveSelector(method)

	if len(p.DefaultParams.Args) == 0 {
		switch method.Name {
		case "burnForPower":
			if strings.TrimSpace(p.DefaultParams.BurnAmount) == "" {
				p.DefaultParams.BurnAmount = "500000000000"
			}
			p.DefaultParams.Args = []string{p.DefaultParams.BurnAmount}
		case "claim":
			if p.DefaultParams.ClaimArg == 0 {
				p.DefaultParams.ClaimArg = 1
			}
			p.DefaultParams.Args = []string{strconv.FormatInt(p.DefaultParams.ClaimArg, 10)}
		default:
			if len(method.Inputs) > 0 {
				return fmt.Errorf("defaultParams.args required (%d inputs)", len(method.Inputs))
			}
		}
	}
	if len(p.DefaultParams.Args) != len(method.Inputs) {
		return fmt.Errorf("defaultParams.args: want %d got %d", len(method.Inputs), len(p.DefaultParams.Args))
	}
	// dry-run pack with defaults
	if _, err := PackCalldata(*p, tasks.TaskParams{}); err != nil {
		return fmt.Errorf("abi args: %w", err)
	}
	return nil
}
