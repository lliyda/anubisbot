package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"anubisbot/internal/api"
	"anubisbot/internal/batch"
	"anubisbot/internal/config"
	"anubisbot/internal/logstore"
	"anubisbot/internal/rpc"
	"anubisbot/internal/scheduler"
	"anubisbot/internal/settings"
	"anubisbot/internal/tasks"
	"anubisbot/internal/timesync"
	"anubisbot/internal/version"
	"anubisbot/internal/wallets"
)

func main() {
	mustChdir()
	env := config.LoadEnv()
	logs := logstore.NewWithFile(2000, "data/logs.jsonl")
	// Mirror process logs into ops web「运行日志」; panel captures stdout separately.
	log.SetOutput(io.MultiWriter(os.Stdout, &logstore.LineWriter{Store: logs, Event: "console"}))
	log.SetFlags(log.LstdFlags)
	if seeded, err := config.EnsureRuntime("."); err != nil {
		log.Fatal(err)
	} else {
		for _, rel := range seeded {
			log.Printf("seeded missing config: %s", rel)
		}
	}
	log.Printf("anubisbot %s starting MODE=%s HTTP=%s", version.String(), env.Mode, env.HTTPAddr)

	chain, err := config.LoadChain(env.ChainConfigPath)
	if err != nil {
		log.Fatal(err)
	}
	presets, err := config.LoadPresets(env.PresetsPath)
	if err != nil {
		log.Fatal(err)
	}
	syncer := timesync.New()
	rpcMgr, err := rpc.New(chain, env.ChainConfigPath, syncer)
	if err != nil {
		log.Printf("rpc initial dial failed: %v — continuing, ping will retry", err)
		rpcMgr = rpc.NewSoft(chain, env.ChainConfigPath, syncer)
	}
	// persist activeRpcIds migration / defaults
	_ = config.SaveChain(env.ChainConfigPath, chain)
	config.MigrateLegacyModeFiles()

	simWalletsPath, simSecretsPath, simTasksPath := config.ModeDataPaths("sim")
	liveWalletsPath, liveSecretsPath, liveTasksPath := config.ModeDataPaths("live")
	wSim, err := wallets.LoadWithSecrets(simWalletsPath, simSecretsPath)
	if err != nil {
		log.Fatal(err)
	}
	wLive, err := wallets.LoadWithSecrets(liveWalletsPath, liveSecretsPath)
	if err != nil {
		log.Fatal(err)
	}
	tSim, err := tasks.Load(simTasksPath)
	if err != nil {
		log.Fatal(err)
	}
	tLive, err := tasks.Load(liveTasksPath)
	if err != nil {
		log.Fatal(err)
	}
	// seed defaults only in SIM
	{
		var ref tasks.WalletRef
		if w, ok := wSim.Get("w1"); ok {
			ref = tasks.WalletRef{ID: w.ID, Name: w.Name, Address: w.Address}
		} else if list := wSim.ListPublic(); len(list) > 0 {
			ref = tasks.WalletRef{ID: list[0].ID, Name: list[0].Name, Address: list[0].Address}
		}
		if n, err := tasks.EnsureDefaults(tSim, ref, presets); err != nil {
			log.Printf("ensure default tasks: %v", err)
		} else if n > 0 {
			log.Printf("seeded %d default sim tasks", n)
		}
		if n, err := tSim.SyncAllSchedules(presets, time.Now()); err != nil {
			log.Printf("sync sim task schedules: %v", err)
		} else if n > 0 {
			log.Printf("synced schedule on %d sim task(s)", n)
		}
		if n, err := tSim.NormalizeAllFireAts(time.Now()); err != nil {
			log.Printf("normalize sim fireAt: %v", err)
		} else if n > 0 {
			log.Printf("normalized fireAt on %d sim task(s)", n)
		}
		if n, err := tLive.NormalizeAllFireAts(time.Now()); err != nil {
			log.Printf("normalize live fireAt: %v", err)
		} else if n > 0 {
			log.Printf("normalized fireAt on %d live task(s)", n)
		}
	}
	stStore, err := settings.Load("data/settings.json")
	if err != nil {
		log.Fatal(err)
	}
	st := stStore.Get()
	// env overrides defaults on first boot values if settings still default-ish
	if env.SignLeadMs > 0 && st.SignLeadMs == settings.Defaults().SignLeadMs {
		st.SignLeadMs = env.SignLeadMs
	}
	if env.BatchConcurrency > 0 && st.BatchConcurrency == settings.Defaults().BatchConcurrency {
		st.BatchConcurrency = env.BatchConcurrency
	}
	_, _ = stStore.Update(st)
	st = stStore.Get()

	// 优先恢复上次保存的模式；无记录时再用环境变量 MODE
	mode := st.Mode
	if mode != "sim" && mode != "live" {
		mode = env.Mode
	}
	if mode != "live" {
		mode = "sim"
	}
	wstore, tstore := wSim, tSim
	if mode == "live" {
		wstore, tstore = wLive, tLive
	}

	runner := &batch.Runner{
		Mode:          mode,
		Concurrency:   st.BatchConcurrency,
		RPC:           rpcMgr,
		Wallets:       wstore,
		Tasks:         tstore,
		Presets:       presets,
		Logs:          logs,
		GasMultiplier: st.GasMultiplier,
	}
	sched := &scheduler.Scheduler{
		Tasks:    tstore,
		Syncer:   syncer,
		Runner:   runner,
		Logs:     logs,
		RPC:      rpcMgr,
		Settings: stStore,
	}

	srvAPI := &api.Server{
		Env:         env,
		ChainPath:   env.ChainConfigPath,
		Chain:       chain,
		Presets:     presets,
		RPC:         rpcMgr,
		Syncer:      syncer,
		Wallets:     wstore,
		Tasks:       tstore,
		WalletsSim:  wSim,
		WalletsLive: wLive,
		TasksSim:    tSim,
		TasksLive:   tLive,
		Logs:        logs,
		Sched:       sched,
		Runner:      runner,
		Settings:    stStore,
		WebFS:       os.DirFS("web"),
	}
	srvAPI.ApplyMode(mode)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rpcMgr.StartPinger(ctx)
	syncer.ConfigureNTP(st.NtpHost, st.NtpIntervalSec)
	syncer.StartNTP(ctx) // 毫秒级时钟偏差（NTP），默认 30s，可在设置中改
	sched.Start(ctx)

	// 启动时各模式拉一次余额缓存；打开网页不再打链上余额
	go func() {
		time.Sleep(1500 * time.Millisecond)
		log.Printf("refreshing wallet balance cache (startup once)")
		srvAPI.RefreshBalanceCache("sim")
		srvAPI.RefreshBalanceCache("live")
		log.Printf("wallet balance cache ready")
	}()

	httpSrv := &http.Server{
		Addr:              env.HTTPAddr,
		Handler:           srvAPI.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("listening on http://%s", env.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	sched.Stop()
	shctx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = httpSrv.Shutdown(shctx)
}

func mustChdir() {
	if _, err := os.Stat("config/chain.json"); err == nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	for _, c := range []string{dir, filepath.Join(dir, ".."), filepath.Join(dir, "../..")} {
		if _, err := os.Stat(filepath.Join(c, "config/chain.json")); err == nil {
			_ = os.Chdir(c)
			return
		}
	}
	// Bare install (exe-only zip): use directory of the executable and seed defaults later.
	_ = os.Chdir(dir)
}
