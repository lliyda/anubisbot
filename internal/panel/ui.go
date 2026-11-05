//go:build windows

package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"anubisbot/internal/version"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// RunUI starts the native tray + control window. Blocks until the panel exits.
// Does not stop the bot process on quit.
func RunUI(store *Store, bot *BotManager) {
	lockLn, err := net.Listen("tcp", "127.0.0.1:47191")
	if err != nil {
		if c, dialErr := net.DialTimeout("tcp", "127.0.0.1:47191", time.Second); dialErr == nil {
			_, _ = c.Write([]byte("show\n"))
			_ = c.Close()
		}
		walk.MsgBox(nil, "Anubisbot 面板",
			"面板已在运行。\n请看任务栏右下角托盘图标（可能在 ^ 隐藏图标里）。\n左键打开控制窗。",
			walk.MsgBoxIconInformation)
		return
	}
	defer lockLn.Close()

	var (
		mw        *walk.MainWindow
		ni        *walk.NotifyIcon
		statusLbl *walk.Label
		infoEdit  *walk.TextEdit
		busyMu    sync.Mutex
		busy      bool
		showMu    sync.Mutex
	)

	setBusy := func(v bool) {
		busyMu.Lock()
		busy = v
		busyMu.Unlock()
	}
	isBusy := func() bool {
		busyMu.Lock()
		defer busyMu.Unlock()
		return busy
	}

	setInfo := func(s string) {
		if infoEdit != nil {
			infoEdit.SetText(s)
		}
	}

	refreshStatus := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		h, err := bot.FetchHealth(ctx)
		text := "● 离线 / 不可达"
		if err == nil {
			ver, _ := h["version"].(string)
			mode, _ := h["mode"].(string)
			text = fmt.Sprintf("● 运行中  v%s  mode=%v  offset=%vms", ver, mode, h["offsetMs"])
		}
		if mw != nil {
			mw.Synchronize(func() {
				if statusLbl != nil {
					_ = statusLbl.SetText(text)
				}
			})
		}
	}

	runAsync := func(fn func() error, okMsg string) {
		if isBusy() {
			return
		}
		setBusy(true)
		go func() {
			err := fn()
			setBusy(false)
			if mw != nil {
				mw.Synchronize(func() {
					if err != nil {
						walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
					} else if okMsg != "" {
						walk.MsgBox(mw, "完成", okMsg, walk.MsgBoxIconInformation)
					}
					refreshStatus()
				})
			}
		}()
	}

	openOps := func() {
		url := strings.TrimRight(store.Get().BotHTTP, "/") + "/"
		if err := OpenBrowser(url); err != nil {
			walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
			return
		}
		bot.AppendLog("opened " + url)
		refreshStatus()
	}

	showWindow := func() {
		showMu.Lock()
		defer showMu.Unlock()
		if mw != nil {
			mw.Show()
			_ = mw.SetFocus()
		}
	}

	openConfig := func() {
		cfg := store.Get()
		var (
			dlg       *walk.Dialog
			repoEdit  *walk.LineEdit
			assetEdit *walk.LineEdit
			httpEdit  *walk.LineEdit
			exeEdit   *walk.LineEdit
			acceptPB  *walk.PushButton
		)
		_, err := Dialog{
			AssignTo:      &dlg,
			Title:         "面板配置",
			MinSize:       Size{Width: 420, Height: 220},
			Size:          Size{Width: 460, Height: 240},
			Layout:        VBox{},
			DefaultButton: &acceptPB,
			Children: []Widget{
				Composite{
					Layout: Grid{Columns: 2, Spacing: 8},
					Children: []Widget{
						Label{Text: "GitHub 仓库"},
						LineEdit{AssignTo: &repoEdit, Text: cfg.GithubRepo},
						Label{Text: "资产名"},
						LineEdit{AssignTo: &assetEdit, Text: cfg.AssetName},
						Label{Text: "服务地址"},
						LineEdit{AssignTo: &httpEdit, Text: cfg.BotHTTP},
						Label{Text: "Bot exe"},
						LineEdit{AssignTo: &exeEdit, Text: cfg.BotExe},
					},
				},
				Composite{
					Layout: HBox{},
					Children: []Widget{
						HSpacer{},
						PushButton{AssignTo: &acceptPB, Text: "保存", OnClicked: func() {
							_, err := store.Update(Config{
								GithubRepo: strings.TrimSpace(repoEdit.Text()),
								AssetName:  strings.TrimSpace(assetEdit.Text()),
								BotHTTP:    strings.TrimSpace(httpEdit.Text()),
								BotExe:     strings.TrimSpace(exeEdit.Text()),
							})
							if err != nil {
								walk.MsgBox(dlg, "错误", err.Error(), walk.MsgBoxIconError)
								return
							}
							bot.AppendLog("config saved")
							dlg.Accept()
						}},
						PushButton{Text: "取消", OnClicked: func() { dlg.Cancel() }},
					},
				},
			},
		}.Run(mw)
		if err != nil {
			walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
		}
	}

	go func() {
		for {
			c, err := lockLn.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 32)
			n, _ := c.Read(buf)
			_ = c.Close()
			if n > 0 && strings.Contains(strings.ToLower(string(buf[:n])), "show") {
				if mw != nil {
					mw.Synchronize(showWindow)
				}
			}
		}
	}()

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "Anubisbot 面板  " + version.String(),
		MinSize:  Size{Width: 380, Height: 320},
		Size:     Size{Width: 400, Height: 360},
		Layout:   VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 10},
		Children: []Widget{
			Label{AssignTo: &statusLbl, Text: "状态检测中…"},
			Composite{
				Layout: HBox{Spacing: 6},
				Children: []Widget{
					PushButton{Text: "打开网页", OnClicked: openOps},
					PushButton{Text: "启动", OnClicked: func() {
						runAsync(func() error { return bot.Start() }, "服务已启动")
					}},
					PushButton{Text: "停止", OnClicked: func() {
						runAsync(func() error { return bot.Stop() }, "服务已停止")
					}},
					PushButton{Text: "重启", OnClicked: func() {
						runAsync(func() error { return bot.Restart() }, "服务已重启")
					}},
					PushButton{Text: "刷新", OnClicked: func() { go refreshStatus() }},
				},
			},
			Composite{
				Layout: HBox{Spacing: 6},
				Children: []Widget{
					PushButton{Text: "检查更新", OnClicked: func() {
						if isBusy() {
							return
						}
						setBusy(true)
						go func() {
							ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
							defer cancel()
							info, err := bot.CheckOTA(ctx)
							setBusy(false)
							mw.Synchronize(func() {
								if err != nil {
									setInfo(err.Error())
									walk.MsgBox(mw, "OTA", err.Error(), walk.MsgBoxIconError)
								} else {
									b, _ := json.MarshalIndent(info, "", "  ")
									setInfo(string(b))
									msg := "已是最新 " + info.Current
									if info.Update {
										msg = "发现更新 " + info.TagName
									}
									walk.MsgBox(mw, "OTA", msg, walk.MsgBoxIconInformation)
								}
								refreshStatus()
							})
						}()
					}},
					PushButton{Text: "应用更新", OnClicked: func() {
						if walk.MsgBox(mw, "OTA", "将下载 Release、停止服务并替换 anubisbot.exe，继续？", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
							return
						}
						runAsync(func() error {
							ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
							defer cancel()
							info, err := bot.ApplyOTA(ctx)
							if info != nil {
								b, _ := json.MarshalIndent(info, "", "  ")
								mw.Synchronize(func() { setInfo(string(b)) })
							}
							return err
						}, "更新完成")
					}},
					HSpacer{},
					PushButton{Text: "配置…", OnClicked: openConfig},
				},
			},
			TextEdit{AssignTo: &infoEdit, ReadOnly: true, VScroll: true, Text: "尚未检查更新", MinSize: Size{Height: 180}},
		},
	}.Create()); err != nil {
		panic(err)
	}

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = true
		mw.Hide()
	})

	icon, err := walk.NewIconFromResourceId(2)
	if err != nil {
		icon, err = walk.NewIconFromFile("anubisbot-panel.exe")
	}
	if err != nil {
		icon, _ = walk.Resources.Icon("3")
	}
	ni, err = walk.NewNotifyIcon(mw)
	if err != nil {
		panic(err)
	}
	if icon != nil {
		_ = ni.SetIcon(icon)
	}
	_ = ni.SetToolTip("Anubisbot 面板 " + version.String())
	_ = ni.SetVisible(true)

	addTray := func(text string, fn func()) {
		act := walk.NewAction()
		_ = act.SetText(text)
		act.Triggered().Attach(fn)
		ni.ContextMenu().Actions().Add(act)
	}
	addTray("打开运维页", openOps)
	addTray("启动服务", func() { runAsync(func() error { return bot.Start() }, "") })
	addTray("停止服务", func() { runAsync(func() error { return bot.Stop() }, "") })
	addTray("重启服务", func() { runAsync(func() error { return bot.Restart() }, "") })
	ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	addTray("打开控制窗", showWindow)
	addTray("配置…", openConfig)
	ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	addTray("退出面板", func() {
		_ = ni.Dispose()
		walk.App().Exit(0)
	})

	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			showWindow()
		}
	})

	go func() {
		refreshStatus()
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for range t.C {
			refreshStatus()
		}
	}()

	mw.Show()
	mw.Run()
}
