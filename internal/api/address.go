package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"sync"

	"anubisbot/internal/helixchain"
	"anubisbot/internal/helixfetch"
	"anubisbot/internal/tokenbal"

	"github.com/go-chi/chi/v5"
)

var addrRE = regexp.MustCompile(`(?i)^0x[0-9a-f]{40}$`)

type fetchErr struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

func (s *Server) handleAddress(w http.ResponseWriter, r *http.Request) {
	addr := chi.URLParam(r, "addr")
	if !addrRE.MatchString(addr) {
		writeErr(w, 400, "invalid address")
		return
	}
	ctx := r.Context()

	type slot struct {
		key string
		url string
	}
	slots := []slot{
		{"power", helixfetch.PowerURL(addr)},
		{"emission", helixfetch.EmissionURL(addr)},
		{"community", helixfetch.CommunityURL(addr)},
		{"level", helixfetch.LevelURL(addr)},
		{"turbine", helixfetch.TurbineURL(addr)},
		{"ve", helixfetch.VeURL(addr)},
		{"rewards", helixfetch.RewardsURL(addr)},
		{"activity", helixfetch.ActivityURL(addr, 40, 0)},
		{"nft", helixfetch.NFTURL(addr)},
		{"claims", helixfetch.ClaimsURL(addr)},
	}

	out := map[string]any{
		"address": addr,
	}
	var errs []fetchErr
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, sl := range slots {
		sl := sl
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := helixfetch.GetJSON(ctx, sl.url)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out[sl.key] = nil
				errs = append(errs, fetchErr{Source: sl.key, Message: err.Error()})
				return
			}
			out[sl.key] = v
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		bal := s.fetchOneAddressBalances(ctx, addr)
		mu.Lock()
		out["balances"] = bal
		if ok, _ := bal["ok"].(bool); !ok {
			errs = append(errs, fetchErr{Source: "balances", Message: "rpc balance fetch incomplete"})
		}
		mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		cli, err := s.RPC.Client()
		if err != nil || s.Chain == nil {
			mu.Lock()
			out["burnGate"] = nil
			out["ailgnsPriceDai"] = nil
			out["networkActivePower"] = nil
			errs = append(errs, fetchErr{Source: "helixchain", Message: "rpc unavailable"})
			mu.Unlock()
			return
		}
		cc := s.Chain.Contracts
		if cc == nil {
			cc = map[string]string{}
		}
		snap := helixchain.Load(ctx, cli, helixchain.Contracts{
			Gate:   cc["lgnsBurnGate"],
			Ledger: cc["powerLedger"],
			Clock:  cc["epochClock"],
			Pair:   cc["ailgnsDaiPair"],
			LGNS:   s.Chain.Tokens["lgns"],
			AiLGNS: s.Chain.Tokens["ailgns"],
			DAI:    s.Chain.Tokens["dai"],
		}, addr)
		mu.Lock()
		out["burnGate"] = snap.BurnGate
		out["ailgnsPriceDai"] = snap.AilgnsPriceDai
		out["networkActivePower"] = snap.NetworkActivePower
		out["_longStake"] = snap.LongStake
		for _, msg := range snap.Errs {
			errs = append(errs, fetchErr{Source: "helixchain", Message: msg})
		}
		mu.Unlock()
	}()

	wg.Wait()
	if bal, ok := out["balances"].(map[string]any); ok {
		if ls, _ := out["_longStake"].(string); ls != "" {
			bal["longStake"] = ls
		}
	}
	delete(out, "_longStake")
	out["errors"] = errs
	writeJSON(w, 200, out)
}

func (s *Server) handleAddressActivity(w http.ResponseWriter, r *http.Request) {
	addr := chi.URLParam(r, "addr")
	if !addrRE.MatchString(addr) {
		writeErr(w, 400, "invalid address")
		return
	}
	limit := 40
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	v, err := helixfetch.GetJSON(r.Context(), helixfetch.ActivityURL(addr, limit, offset))
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) fetchOneAddressBalances(ctx context.Context, address string) map[string]any {
	entry := map[string]any{
		"dai": "—", "lgns": "—", "slgns": "—", "ailgns": "—",
		"longStake": nil, "ok": false,
	}
	if s.Chain == nil {
		return entry
	}
	dai := s.Chain.Tokens["dai"]
	lgns := s.Chain.Tokens["lgns"]
	slgns := s.Chain.Tokens["slgns"]
	ailgns := s.Chain.Tokens["ailgns"]
	cli, err := s.RPC.Client()
	if err != nil || dai == "" || lgns == "" {
		return entry
	}

	okAll := true
	daiBal, err1 := tokenbal.BalanceOf(ctx, cli, dai, address)
	if err1 == nil {
		entry["dai"] = tokenbal.Format(daiBal, tokenbal.Decimals(ctx, cli, dai, 18))
	} else {
		okAll = false
	}
	lgnsBal, err2 := tokenbal.BalanceOf(ctx, cli, lgns, address)
	if err2 == nil {
		entry["lgns"] = tokenbal.Format(lgnsBal, tokenbal.Decimals(ctx, cli, lgns, 9))
	} else {
		okAll = false
	}
	if slgns != "" {
		if bal, e := tokenbal.BalanceOf(ctx, cli, slgns, address); e == nil {
			entry["slgns"] = tokenbal.Format(bal, tokenbal.Decimals(ctx, cli, slgns, 9))
		} else {
			okAll = false
		}
	}
	if ailgns != "" {
		if bal, e := tokenbal.BalanceOf(ctx, cli, ailgns, address); e == nil {
			entry["ailgns"] = tokenbal.Format(bal, tokenbal.Decimals(ctx, cli, ailgns, 9))
		} else {
			okAll = false
		}
	}
	entry["ok"] = okAll
	return entry
}
