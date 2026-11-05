package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"anubisbot/internal/panel"
	"anubisbot/internal/version"
)

func main() {
	httpFlag := flag.Bool("http", false, "also serve legacy web panel (debug)")
	flag.Parse()

	root, err := panel.EnsureRoot()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		log.Fatal(err)
	}

	cfgPath := filepath.Join(root, "data", "panel.json")
	store, err := panel.Load(root, cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	bot := panel.NewBotManager(store)

	fmt.Printf("anubisbot-panel %s (native tray)\n", version.String())

	go func() {
		time.Sleep(400 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if !bot.Healthy(ctx) {
			if err := bot.Start(); err != nil {
				bot.AppendLog("auto-start: " + err.Error())
			}
		} else {
			bot.AppendLog("bot already healthy")
		}
	}()

	if *httpFlag {
		go func() {
			srv := &panel.Server{Cfg: store, Bot: bot, WebFS: os.DirFS("web")}
			addr := store.Get().Listen
			fmt.Printf("debug HTTP panel on http://%s\n", addr)
			if err := http.ListenAndServe(addr, srv.Router()); err != nil {
				log.Printf("http panel: %v", err)
			}
		}()
	}

	panel.RunUI(store, bot)
}
