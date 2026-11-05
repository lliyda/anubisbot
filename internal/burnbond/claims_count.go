package burnbond

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const CountWalletURL = "https://count.web3origin.com/wallet"

var countHTTP = &http.Client{Timeout: 8 * time.Second}

type countWalletResp struct {
	OK        bool            `json:"ok"`
	Transfers []countTransfer `json:"transfers"`
}

type countTransfer struct {
	Ts     int64   `json:"ts"`
	Token  string  `json:"token"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	Amount float64 `json:"amount"`
	Dir    string  `json:"dir"`
	Hash   string  `json:"hash"`
}

// FetchClaimsFromCount loads recent fixed-price payouts via count.web3origin.com.
func FetchClaimsFromCount(ctx context.Context, addr string) ([]Claim, error) {
	if !common.IsHexAddress(addr) {
		return nil, fmt.Errorf("invalid address")
	}
	lower := strings.ToLower(common.HexToAddress(addr).Hex())
	q := url.Values{}
	q.Set("chain", "anubis")
	q.Set("addr", lower)
	endpoint := CountWalletURL + "?" + q.Encode()
	form := url.Values{}
	form.Set("chain", "anubis")
	form.Set("addr", lower)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "anubisbot/1.0")

	res, err := countHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 160 {
			msg = msg[:160]
		}
		return nil, fmt.Errorf("http %d: %s", res.StatusCode, msg)
	}
	var parsed countWalletResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if !parsed.OK {
		return nil, fmt.Errorf("count api ok=false")
	}

	payout := strings.ToLower(common.HexToAddress(PayoutFixed).Hex())
	out := make([]Claim, 0, 8)
	for _, t := range parsed.Transfers {
		if !strings.EqualFold(t.Token, "LGNS") {
			continue
		}
		if !strings.EqualFold(t.Dir, "in") {
			continue
		}
		if strings.ToLower(common.HexToAddress(t.From).Hex()) != payout {
			continue
		}
		at := time.Unix(t.Ts, 0).UTC().Format(time.RFC3339)
		amt := strconv.FormatFloat(t.Amount, 'f', -1, 64)
		out = append(out, Claim{
			At:     at,
			Kind:   "fixed_price",
			Label:  "固定价销毁债券",
			Amount: amt,
			TxHash: t.Hash,
		})
	}
	// API usually returns newest-first; keep stable desc by ts.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].At > out[i].At {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}
