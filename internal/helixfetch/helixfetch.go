package helixfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	BaseAH = "https://awakehelix.io/api/v1"
	BaseZJ = "https://zjgame.web3origin.com/helix-api"
)

var client = &http.Client{Timeout: 8 * time.Second}

// GetJSON fetches url and decodes JSON into a generic map/slice value.
func GetJSON(ctx context.Context, url string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "anubisbot/1.0")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("http %d: %s", res.StatusCode, msg)
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func PowerURL(addr string) string {
	return fmt.Sprintf("%s/power/%s?limit=50", BaseAH, addr)
}
func EmissionURL(addr string) string {
	return fmt.Sprintf("%s/emission/%s", BaseAH, addr)
}
func CommunityURL(addr string) string {
	return fmt.Sprintf("%s/community/%s", BaseAH, addr)
}
func LevelURL(addr string) string {
	return fmt.Sprintf("%s/level/%s", BaseAH, addr)
}
func TurbineURL(addr string) string {
	return fmt.Sprintf("%s/turbine/%s", BaseAH, addr)
}
func VeURL(addr string) string {
	return fmt.Sprintf("%s/ve/%s", BaseAH, addr)
}
func RewardsURL(addr string) string {
	return fmt.Sprintf("%s/rewards/%s", BaseAH, addr)
}
func ActivityURL(addr string, limit, offset int) string {
	return fmt.Sprintf("%s/activity/%s?limit=%d&offset=%d", BaseAH, addr, limit, offset)
}
func NFTURL(addr string) string {
	return fmt.Sprintf("%s/nft/%s?limit=20", BaseAH, addr)
}
func ClaimsURL(addr string) string {
	return fmt.Sprintf("%s/claims/%s", BaseZJ, strings.ToLower(addr))
}
