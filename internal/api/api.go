package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"anubisbot/internal/batch"
	"anubisbot/internal/config"
	"anubisbot/internal/logstore"
	"anubisbot/internal/rpc"
	"anubisbot/internal/scheduler"
	"anubisbot/internal/settings"
	"anubisbot/internal/tasks"
	"anubisbot/internal/timesync"
	"anubisbot/internal/tokenbal"
	"anubisbot/internal/txbuild"
	"anubisbot/internal/version"
	"anubisbot/internal/wallets"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var presetIDRE = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)
var ethAddrRE = regexp.MustCompile(`(?i)^0x[0-9a-f]{40}$`)

type Server struct {
	Env       config.Env
	Mode      atomic.Value // string
	ChainPath string
	Chain     *config.ChainConfig
	Presets   map[string]config.Preset
	presetsMu sync.Mutex
	RPC       *rpc.Manager
	Syncer    *timesync.Sync
	Wallets   *wallets.Store
	Tasks     *tasks.Store
	WalletsSim  *wallets.Store
	WalletsLive *wallets.Store
	TasksSim    *tasks.Store
	TasksLive   *tasks.Store
	storeMu   sync.Mutex
	balMu       sync.RWMutex
	balCacheSim map[string]any
	balCacheLive map[string]any
	Logs      *logstore.Store
	Sched     *scheduler.Scheduler
	Runner    *batch.Runner
	Settings  *settings.Store
	WebFS     fs.FS
}

// ApplyMode switches active wallet/task stores for SIM/LIVE isolation.
func (s *Server) ApplyMode(mode string) {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if mode != "live" {
		mode = "sim"
	}
	s.Mode.Store(mode)
	if s.Runner != nil {
		s.Runner.Mode = mode
	}
	if mode == "live" {
		s.Wallets = s.WalletsLive
		s.Tasks = s.TasksLive
	} else {
		s.Wallets = s.WalletsSim
		s.Tasks = s.TasksSim
	}
	if s.Runner != nil {
		s.Runner.Wallets = s.Wallets
		s.Runner.Tasks = s.Tasks
	}
	if s.Sched != nil {
		s.Sched.Tasks = s.Tasks
	}
}

func (s *Server) walletStoreForMode(mode string) *wallets.Store {
	if mode == "live" {
		return s.WalletsLive
	}
	return s.WalletsSim
}

func (s *Server) cachedBalances(mode string) map[string]any {
	s.balMu.RLock()
	defer s.balMu.RUnlock()
	var src map[string]any
	if mode == "live" {
		src = s.balCacheLive
	} else {
		src = s.balCacheSim
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// RefreshBalanceCache fetches on-chain balances once for the given mode and caches them.
func (s *Server) RefreshBalanceCache(mode string) {
	if mode != "live" {
		mode = "sim"
	}
	store := s.walletStoreForMode(mode)
	if store == nil {
		return
	}
	list := store.ListPublic()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	bal := s.fetchBalances(ctx, list)
	s.balMu.Lock()
	if mode == "live" {
		s.balCacheLive = bal
	} else {
		s.balCacheSim = bal
	}
	s.balMu.Unlock()
}

func (s *Server) refreshBalanceCacheAsync() {
	mode := s.mode()
	go s.RefreshBalanceCache(mode)
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(s.auth)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/mode", s.handleGetMode)
		r.Put("/mode", s.handlePutMode)
		r.Get("/settings", s.handleGetSettings)
		r.Put("/settings", s.handlePutSettings)
		r.Get("/rpc", s.handleListRPC)
		r.Put("/rpc", s.handleReplaceRPC)
		r.Put("/rpc/active", s.handleSetRPC)
		r.Post("/rpc/probe", s.handleProbeRPC)
		r.Post("/rpc", s.handleAddRPC)
		r.Put("/rpc/{id}", s.handleUpdateRPC)
		r.Delete("/rpc/{id}", s.handleDelRPC)
		r.Get("/presets", s.handlePresets)
		r.Put("/presets/{id}", s.handlePutPreset)
		r.Delete("/presets/{id}", s.handleDeletePreset)
		r.Get("/wallets", s.handleWallets)
		r.Post("/wallets", s.handleAddWallet)
		r.Post("/wallets/import", s.handleImportWallet)
		r.Delete("/wallets/{id}", s.handleDeleteWallet)
		r.Get("/tasks", s.handleListTasks)
		r.Post("/tasks", s.handleCreateTask)
		r.Patch("/tasks/{id}", s.handlePatchTask)
		r.Delete("/tasks/{id}", s.handleDeleteTask)
		r.Post("/tasks/{id}/enable", s.handleEnableTask)
		r.Post("/tasks/{id}/arm", s.handleArmTask)
		r.Post("/tasks/{id}/cancel", s.handleCancelTask)
		r.Post("/tasks/{id}/dry-run", s.handleDryRun)
		r.Post("/tasks/{id}/run-once", s.handleRunOnce)
		r.Post("/tasks/{id}/simulate", s.handleSimulate)
		r.Post("/simulate", s.handleSimulatePreset)
		r.Get("/tasks/{id}/lgns-allowance", s.handleTaskLgnsAllowance)
		r.Post("/tasks/{id}/lgns-approve", s.handleTaskLgnsApprove)
		r.Get("/logs", s.handleLogs)
		r.Get("/address/{addr}", s.handleAddress)
		r.Get("/address/{addr}/rpc", s.handleAddressRPC)
		r.Get("/address/{addr}/helix", s.handleAddressHelix)
		r.Get("/address/{addr}/burn-claims", s.handleAddressBurnClaims)
		r.Get("/address/{addr}/activity", s.handleAddressActivity)
	})

	if s.WebFS != nil {
		ops, _ := fs.Sub(s.WebFS, "ops")
		wallet, _ := fs.Sub(s.WebFS, "wallet")
		r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(ops))))
		r.Get("/", func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, ops, "index.html")
		})
		r.Get("/app.css", func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, ops, "app.css")
		})
		r.Get("/app.js", func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, ops, "app.js")
		})
		r.Get("/address.css", func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, ops, "address.css")
		})
		r.Get("/address.js", func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, ops, "address.js")
		})
		r.Get("/address.html", func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, ops, "address.html")
		})
		r.Get("/wallet/", func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, wallet, "index.html")
		})
		r.Get("/wallet/app.js", func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, wallet, "app.js")
		})
		r.Get("/wallet/app.css", func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, wallet, "app.css")
		})
	}
	return r
}

func serveFile(w http.ResponseWriter, fsys fs.FS, name string) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	_, _ = w.Write(b)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Env.OpsToken == "" || !strings.HasPrefix(r.URL.Path, "/api") {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != s.Env.OpsToken {
			writeErr(w, 401, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) mode() string {
	v, _ := s.Mode.Load().(string)
	if v == "" {
		return "sim"
	}
	return v
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) applySettings(st settings.Settings) {
	s.Runner.Concurrency = st.BatchConcurrency
	s.Runner.GasMultiplier = st.GasMultiplier
	if s.Syncer != nil {
		s.Syncer.ConfigureNTP(st.NtpHost, st.NtpIntervalSec)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	now := s.Syncer.NowChain()
	off, rtt, _ := s.Syncer.Snapshot()
	st := s.Settings.Get()
	sendLead := int64(0)
	leads := map[string]int64{}
	if s.Sched != nil {
		sendLead = s.Sched.CurrentLeadMs()
		leads = s.Sched.CurrentLeads()
	}
	writeJSON(w, 200, map[string]any{
		"version":     version.String(),
		"mode":        s.mode(),
		"chainId":     s.Chain.ChainID,
		"explorer":    s.Chain.Explorer,
		"activeRpc":   s.RPC.Primary(),
		"activeIds":   s.RPC.ActiveIDs(),
		"rttMs":       rtt,
		"offsetMs":    off,
		"rttGreen":    s.Chain.RTTGreenMs,
		"rttYellow":   s.Chain.RTTYellowMs,
		"nextHour":    timesync.NextHourCST(now),
		"next1159":    timesync.Next1159CST(now, 0),
		"nowChain":    now,
		"rpcsRtt":     s.RPC.RTTs(),
		"leads":       leads,
		"sendLeadMs":  sendLead,
		"sendSlackMs": st.SendSlackMs,
		"signLeadMs":  st.SignLeadMs,
	})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st := s.Settings.Get()
	writeJSON(w, 200, map[string]any{
		"signLeadMs":       st.SignLeadMs,
		"sendSlackMs":      st.SendSlackMs,
		"batchConcurrency": st.BatchConcurrency,
		"gasMultiplier":    st.GasMultiplier,
		"ntpHost":          st.NtpHost,
		"ntpIntervalSec":   st.NtpIntervalSec,
		"mode":             s.mode(),
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body settings.Settings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	st, err := s.Settings.Update(body)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.applySettings(st)
	s.Logs.Add(logstore.Entry{Event: "settings_updated", Message: "runtime settings saved"})
	writeJSON(w, 200, st)
}

func (s *Server) handleGetMode(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"mode": s.mode()})
}

func (s *Server) handlePutMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode    string `json:"mode"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.Mode == "live" && !body.Confirm {
		writeErr(w, 400, "live requires confirm:true")
		return
	}
	if body.Mode != "sim" && body.Mode != "live" {
		writeErr(w, 400, "mode must be sim|live")
		return
	}
	s.ApplyMode(body.Mode)
	if s.Settings != nil {
		_ = s.Settings.SetMode(body.Mode)
	}
	s.Logs.Add(logstore.Entry{Event: "mode", Message: "switched to " + body.Mode + " (wallets/tasks isolated)"})
	writeJSON(w, 200, map[string]string{"mode": body.Mode})
}

func (s *Server) handleListRPC(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"activeId":  s.RPC.PrimaryID(),
		"activeIds": s.RPC.ActiveIDs(),
		"rpcs":      s.RPC.List(),
		"rtts":      s.RPC.RTTs(),
		"leads":     s.RPC.Leads(),
		"rttGreen":  s.Chain.RTTGreenMs,
		"rttYellow": s.Chain.RTTYellowMs,
	})
}

func (s *Server) handleSetRPC(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID  string   `json:"id"`
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	ids := body.IDs
	if len(ids) == 0 && body.ID != "" {
		ids = []string{body.ID}
	}
	if err := s.RPC.SetActiveIDs(ids); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	_ = s.RPC.PingActive(r.Context())
	writeJSON(w, 200, map[string]any{"activeIds": s.RPC.ActiveIDs(), "activeId": s.RPC.PrimaryID()})
}

func (s *Server) handleReplaceRPC(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RPCs []config.RPCEndpoint `json:"rpcs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	saved, err := s.RPC.Replace(body.RPCs)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":        "1",
		"rpcs":      saved,
		"activeIds": s.RPC.ActiveIDs(),
		"activeId":  s.RPC.PrimaryID(),
	})
}

func (s *Server) handleProbeRPC(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	normalized, rttMs, err := rpc.ProbeURL(ctx, body.URL)
	if err != nil {
		if normalized == "" {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"ok":    false,
			"url":   normalized,
			"error": err.Error(),
		})
		return
	}
	if s.Syncer != nil {
		s.Syncer.SetRTT(rttMs)
		_ = s.Syncer.SyncNTP() // 手动测延迟时顺带刷新毫秒级 offset
	}
	writeJSON(w, 200, map[string]any{
		"ok":       true,
		"url":      normalized,
		"rttMs":    rttMs,
		"offsetMs": s.Syncer.OffsetMs(),
	})
}

func (s *Server) handleAddRPC(w http.ResponseWriter, r *http.Request) {
	var ep config.RPCEndpoint
	if err := json.NewDecoder(r.Body).Decode(&ep); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	saved, err := s.RPC.Add(ep)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, saved)
}

func (s *Server) handleUpdateRPC(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if err := s.RPC.Update(id, body.Name, body.URL); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1", "id": id})
}

func (s *Server) handleDelRPC(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.RPC.Delete(id); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	s.presetsMu.Lock()
	defer s.presetsMu.Unlock()
	writeJSON(w, 200, s.Presets)
}

func clonePresets(src map[string]config.Preset) map[string]config.Preset {
	next := make(map[string]config.Preset, len(src))
	for k, v := range src {
		next[k] = v
	}
	return next
}

func (s *Server) setPresetsLocked(next map[string]config.Preset) {
	s.Presets = next
	if s.Runner != nil {
		s.Runner.Presets = next
	}
}

func validatePreset(p *config.Preset) string {
	p.Label = strings.TrimSpace(p.Label)
	p.To = strings.TrimSpace(p.To)
	p.Function = strings.TrimSpace(p.Function)
	p.Method = strings.TrimSpace(p.Method)
	p.DefaultSchedule = strings.TrimSpace(p.DefaultSchedule)
	if p.Label == "" {
		return "label required"
	}
	if !ethAddrRE.MatchString(p.To) {
		return "invalid to address"
	}
	switch p.DefaultSchedule {
	case "hourly", "daily_1159_cst", "once":
	default:
		return "defaultSchedule must be hourly, daily_1159_cst, or once"
	}
	if err := txbuild.NormalizePresetFromABI(p); err != nil {
		return err.Error()
	}
	return ""
}

func (s *Server) handlePutPreset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !presetIDRE.MatchString(id) {
		writeErr(w, 400, "invalid preset id")
		return
	}
	var body config.Preset
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if msg := validatePreset(&body); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	s.presetsMu.Lock()
	next := clonePresets(s.Presets)
	next[id] = body
	if err := config.SavePresets(s.Env.PresetsPath, next); err != nil {
		s.presetsMu.Unlock()
		writeErr(w, 500, err.Error())
		return
	}
	s.setPresetsLocked(next)
	s.presetsMu.Unlock()

	synced, err := s.Tasks.SyncScheduleForType(id, body.DefaultSchedule, s.Syncer.NowChain())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	msg := "preset " + id
	if synced > 0 {
		msg += "; synced schedule on tasks"
	}
	s.Logs.Add(logstore.Entry{Event: "preset_saved", Message: msg})
	writeJSON(w, 200, map[string]any{"id": id, "preset": body, "syncedTasks": synced})
}

func (s *Server) handleDeletePreset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !presetIDRE.MatchString(id) {
		writeErr(w, 400, "invalid preset id")
		return
	}
	for _, t := range s.Tasks.List() {
		if t.TaskType == id {
			writeErr(w, 409, "preset in use")
			return
		}
	}
	s.presetsMu.Lock()
	defer s.presetsMu.Unlock()
	if _, ok := s.Presets[id]; !ok {
		writeErr(w, 404, "preset not found")
		return
	}
	next := clonePresets(s.Presets)
	delete(next, id)
	if err := config.SavePresets(s.Env.PresetsPath, next); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.setPresetsLocked(next)
	s.Logs.Add(logstore.Entry{Event: "preset_deleted", Message: "preset " + id})
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleWallets(w http.ResponseWriter, r *http.Request) {
	list := s.Wallets.ListPublic()
	// 余额仅读启动/导入时缓存，打开网页不再打链上 RPC
	writeJSON(w, 200, map[string]any{
		"wallets":  list,
		"balances": s.cachedBalances(s.mode()),
	})
}

func (s *Server) fetchBalances(ctx context.Context, list []wallets.Wallet) map[string]any {
	out := map[string]any{}
	dai := s.Chain.Tokens["dai"]
	lgns := s.Chain.Tokens["lgns"]
	slgns := s.Chain.Tokens["slgns"]
	ailgns := s.Chain.Tokens["ailgns"]
	cli, err := s.RPC.Client()
	if err != nil || dai == "" || lgns == "" {
		for _, w := range list {
			out[w.ID] = map[string]any{"dai": "—", "lgns": "—", "slgns": "—", "ailgns": "—", "ok": false}
		}
		return out
	}
	sem := make(chan struct{}, 4)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, w := range list {
		w := w
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			entry := map[string]any{"dai": "—", "lgns": "—", "slgns": "—", "ailgns": "—", "ok": false}
			daiBal, err1 := tokenbal.BalanceOf(ctx, cli, dai, w.Address)
			lgnsBal, err2 := tokenbal.BalanceOf(ctx, cli, lgns, w.Address)
			var err3, err4 error
			if err1 == nil {
				d := tokenbal.Decimals(ctx, cli, dai, 18)
				entry["dai"] = tokenbal.Format(daiBal, d)
			}
			if err2 == nil {
				d := tokenbal.Decimals(ctx, cli, lgns, 9)
				entry["lgns"] = tokenbal.Format(lgnsBal, d)
			}
			if slgns != "" {
				slgnsBal, e3 := tokenbal.BalanceOf(ctx, cli, slgns, w.Address)
				err3 = e3
				if e3 == nil {
					d := tokenbal.Decimals(ctx, cli, slgns, 9)
					entry["slgns"] = tokenbal.Format(slgnsBal, d)
				}
			}
			if ailgns != "" {
				ailBal, e4 := tokenbal.BalanceOf(ctx, cli, ailgns, w.Address)
				err4 = e4
				if e4 == nil {
					d := tokenbal.Decimals(ctx, cli, ailgns, 9)
					entry["ailgns"] = tokenbal.Format(ailBal, d)
				}
			}
			entry["ok"] = err1 == nil && err2 == nil && (slgns == "" || err3 == nil) && (ailgns == "" || err4 == nil)
			mu.Lock()
			out[w.ID] = entry
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (s *Server) handleAddWallet(w http.ResponseWriter, r *http.Request) {
	var wal wallets.Wallet
	if err := json.NewDecoder(r.Body).Decode(&wal); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if err := s.Wallets.Add(wal); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.refreshBalanceCacheAsync()
	writeJSON(w, 200, wal)
}

func (s *Server) handleImportWallet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode       string `json:"mode"`
		Name       string `json:"name"`
		PrivateKey string `json:"privateKey"`
		Mnemonic   string `json:"mnemonic"`
		Path       string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	var wal wallets.Wallet
	var err error
	switch body.Mode {
	case "privateKey":
		wal, err = s.Wallets.ImportPrivateKey(body.Name, body.PrivateKey)
	case "mnemonic":
		wal, err = s.Wallets.ImportMnemonic(body.Name, body.Mnemonic, body.Path)
	default:
		writeErr(w, 400, "mode must be privateKey|mnemonic")
		return
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.Logs.Add(logstore.Entry{Event: "wallet_import", Wallet: wal.Name, Message: "imported " + wal.ID})
	s.refreshBalanceCacheAsync()
	writeJSON(w, 200, wal)
}

func (s *Server) handleDeleteWallet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	for _, t := range s.Tasks.List() {
		if t.WalletID == id {
			writeErr(w, 400, "wallet in use by tasks")
			return
		}
	}
	if err := s.Wallets.Delete(id); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	s.refreshBalanceCacheAsync()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"tasks": s.Tasks.List()})
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Tasks.Delete(id); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var t tasks.Task
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if wal, ok := s.Wallets.Get(t.WalletID); ok {
		t.WalletName = wal.Name
		t.WalletAddress = wal.Address
	}
	s.presetsMu.Lock()
	presets := s.Presets
	s.presetsMu.Unlock()
	if p, ok := presets[t.TaskType]; ok {
		t.Schedule = p.DefaultSchedule
	} else {
		writeErr(w, 400, "unknown task type")
		return
	}
	for _, existing := range s.Tasks.List() {
		if existing.WalletID == t.WalletID && existing.TaskType == t.TaskType {
			writeErr(w, 409, "该钱包已有相同任务类型")
			return
		}
	}
	created, err := s.Tasks.Create(t, presets)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if created.Enabled {
		if armed, err := s.Tasks.Arm(created.ID, s.Syncer.NowChain(), nil); err == nil {
			created = armed
			s.Logs.Add(logstore.Entry{
				TaskID: created.ID, Wallet: created.WalletName, Event: "signed",
				Message: "queued next run at " + created.FireAt.Format(time.RFC3339),
			})
		}
	}
	writeJSON(w, 200, created)
}

func (s *Server) handlePatchTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	delete(patch, "schedule") // schedule follows task type only
	delete(patch, "taskType")

	if raw, ok := patch["fireAt"]; ok {
		delete(patch, "fireAt")
		var fa time.Time
		switch v := raw.(type) {
		case string:
			var err error
			fa, err = time.Parse(time.RFC3339, v)
			if err != nil {
				fa, err = time.Parse(time.RFC3339Nano, v)
			}
			if err != nil {
				writeErr(w, 400, "invalid fireAt")
				return
			}
		default:
			writeErr(w, 400, "invalid fireAt")
			return
		}
		t, err := s.Tasks.SetOnceFireAt(id, fa)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if len(patch) == 0 {
			writeJSON(w, 200, t)
			return
		}
	}

	t, err := s.Tasks.Patch(id, patch)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleEnableTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if !body.Enabled {
		t, err := s.Tasks.Disable(id)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, t)
		return
	}
	t0, ok := s.Tasks.Get(id)
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	s.presetsMu.Lock()
	p, pok := s.Presets[t0.TaskType]
	s.presetsMu.Unlock()
	if pok && p.DefaultSchedule != "" && t0.Schedule != p.DefaultSchedule {
		if _, err := s.Tasks.SyncScheduleForType(t0.TaskType, p.DefaultSchedule, s.Syncer.NowChain()); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if _, err := s.Tasks.SetEnabled(id, true); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	t, err := s.Tasks.Arm(id, s.Syncer.NowChain(), nil)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleArmTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	now := s.Syncer.NowChain()
	t, err := s.Tasks.Arm(id, now, nil)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.Logs.Add(logstore.Entry{
		TaskID: id, Event: "signed", Wallet: t.WalletName,
		Message: "queued next run at " + t.FireAt.Format(time.RFC3339),
	})
	writeJSON(w, 200, t)
}

// queueOnceArms arms a fire at now+delay without changing schedule.
func (s *Server) queueOnceArms(id, logEvent, logMsg string, delay time.Duration) (tasks.Task, error) {
	now := s.Syncer.NowChain()
	fa := now.Add(delay)
	t, err := s.Tasks.Arm(id, now, &fa)
	if err != nil {
		return tasks.Task{}, err
	}
	t.Enabled = true
	if err := s.Tasks.Update(t); err != nil {
		return tasks.Task{}, err
	}
	s.Logs.Add(logstore.Entry{
		TaskID: id, Event: logEvent, Message: logMsg, Wallet: t.WalletName,
	})
	return t, nil
}

func (s *Server) handleDryRun(w http.ResponseWriter, r *http.Request) {
	if s.mode() != "sim" {
		writeErr(w, 400, "dry-run only in sim mode")
		return
	}
	id := chi.URLParam(r, "id")
	t, err := s.queueOnceArms(id, "dry_run", "5s dry-run queued", 5*time.Second)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleRunOnce(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.queueOnceArms(id, "run_once", "execute immediately", 0)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleSimulate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, ok := s.Tasks.Get(id)
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	var body struct {
		From string `json:"from"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	res := s.Runner.Simulate(r.Context(), t, body.From)
	writeJSON(w, 200, res)
}

func (s *Server) handleSimulatePreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskType string `json:"taskType"`
		From     string `json:"from"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	body.TaskType = strings.TrimSpace(body.TaskType)
	body.From = strings.TrimSpace(body.From)
	if body.TaskType == "" {
		writeErr(w, 400, "taskType required")
		return
	}
	if !ethAddrRE.MatchString(body.From) {
		writeErr(w, 400, "invalid from address")
		return
	}
	s.presetsMu.Lock()
	p, ok := s.Presets[body.TaskType]
	s.presetsMu.Unlock()
	if !ok {
		writeErr(w, 404, "unknown task type")
		return
	}
	params := tasks.TaskParams{
		Args:       append([]string{}, p.DefaultParams.Args...),
		ClaimArg:   p.DefaultParams.ClaimArg,
		BurnAmount: p.DefaultParams.BurnAmount,
		GasLimit:   p.DefaultParams.GasLimit,
	}
	res := s.Runner.SimulatePreset(r.Context(), body.TaskType, p, params, body.From)
	writeJSON(w, 200, res)
}

func neededBurnAmount(t tasks.Task, p config.Preset) *big.Int {
	raw := strings.TrimSpace(t.Params.BurnAmount)
	if raw == "" {
		raw = strings.TrimSpace(p.DefaultParams.BurnAmount)
	}
	if raw == "" {
		raw = "500000000000"
	}
	n := new(big.Int)
	if _, ok := n.SetString(raw, 10); !ok {
		return big.NewInt(500000000000)
	}
	return n
}

func (s *Server) taskLgnsApproveContext(id string) (tasks.Task, config.Preset, string, string, *big.Int, error) {
	t, ok := s.Tasks.Get(id)
	if !ok {
		return tasks.Task{}, config.Preset{}, "", "", nil, errNotFound
	}
	s.presetsMu.Lock()
	p, ok := s.Presets[t.TaskType]
	s.presetsMu.Unlock()
	if !ok {
		return tasks.Task{}, config.Preset{}, "", "", nil, errBadRequest("unknown task type")
	}
	if !p.NeedsLgnsApprove {
		return tasks.Task{}, config.Preset{}, "", "", nil, errBadRequest("task type does not need LGNS approve")
	}
	if s.Chain == nil || s.Chain.Tokens["lgns"] == "" {
		return tasks.Task{}, config.Preset{}, "", "", nil, errBadRequest("lgns token not configured")
	}
	token := s.Chain.Tokens["lgns"]
	spender := p.To
	needed := neededBurnAmount(t, p)
	return t, p, token, spender, needed, nil
}

type apiError struct {
	code int
	msg  string
}

func (e *apiError) Error() string { return e.msg }

var errNotFound = &apiError{404, "task not found"}

func errBadRequest(msg string) *apiError { return &apiError{400, msg} }

func writeAPIErr(w http.ResponseWriter, err error) {
	if ae, ok := err.(*apiError); ok {
		writeErr(w, ae.code, ae.msg)
		return
	}
	writeErr(w, 500, err.Error())
}

func (s *Server) handleTaskLgnsAllowance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, _, token, spender, needed, err := s.taskLgnsApproveContext(id)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	cli, err := s.RPC.Client()
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	allow, err := tokenbal.Allowance(r.Context(), cli, token, t.WalletAddress, spender)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":        allow.Cmp(needed) >= 0,
		"allowance": allow.String(),
		"needed":    needed.String(),
		"spender":   spender,
		"token":     token,
	})
}

func parseApproveAmountLGNS(raw string) (*big.Int, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "无限" || strings.EqualFold(s, "max") || strings.EqualFold(s, "unlimited") {
		return tokenbal.MaxUint256(), nil
	}
	// human LGNS → 9 decimals wei
	neg := false
	if strings.HasPrefix(s, "-") {
		return nil, errBadRequest("amount must be positive")
	}
	parts := strings.SplitN(s, ".", 2)
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	w := new(big.Int)
	if _, ok := w.SetString(whole, 10); !ok {
		return nil, errBadRequest("invalid amount")
	}
	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(9), nil)
	out := new(big.Int).Mul(w, base)
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > 9 {
			frac = frac[:9]
		}
		for len(frac) < 9 {
			frac += "0"
		}
		f := new(big.Int)
		if _, ok := f.SetString(frac, 10); !ok {
			return nil, errBadRequest("invalid amount")
		}
		out.Add(out, f)
	}
	_ = neg
	if out.Sign() <= 0 {
		return nil, errBadRequest("amount must be positive")
	}
	return out, nil
}

func (s *Server) handleTaskLgnsApprove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, _, token, spender, _, err := s.taskLgnsApproveContext(id)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	var body struct {
		Amount string `json:"amount"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	amt, err := parseApproveAmountLGNS(body.Amount)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	res, err := s.Runner.ApproveLGNS(r.Context(), t, token, spender, amt)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.Tasks.Cancel(id)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	failedOnly := q.Get("failedOnly") == "1" || q.Get("failedOnly") == "true"
	writeJSON(w, 200, map[string]any{"logs": s.Logs.List(limit, q.Get("taskId"), failedOnly)})
}
