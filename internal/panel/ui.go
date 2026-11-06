//go:build windows

package panel

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"anubisbot/internal/panel/assets"
	"anubisbot/internal/version"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func loadAppIcon() *walk.Icon {
	// Prefer real .ico for NotifyIcon (most reliable on Windows tray).
	icoPath := filepath.Join(os.TempDir(), "anubisbot-panel-tray.ico")
	if len(assets.IconICO) > 0 {
		if err := os.WriteFile(icoPath, assets.IconICO, 0o644); err == nil {
			if ic, err := walk.NewIconFromFile(icoPath); err == nil {
				return ic
			}
		}
	}
	if len(assets.IconPNG) > 0 {
		img, _, err := image.Decode(bytes.NewReader(assets.IconPNG))
		if err == nil {
			if ic, err := walk.NewIconFromImage(img); err == nil {
				return ic
			}
		}
	}
	if ic, err := walk.NewIconFromSysDLL("shell32", 13); err == nil {
		return ic
	}
	return nil
}

// RunUI starts the native tray + control window. Blocks until the panel exits.
// Does not stop the bot process on quit.
func RunUI(store *Store, bot *BotManager) {
	lockLn, err := net.Listen("tcp", "127.0.0.1:47191")
	if err != nil {
		if c, dialErr := net.DialTimeout("tcp", "127.0.0.1:47191", time.Second); dialErr == nil {
			_, _ = c.Write([]byte("show\n"))
			_ = c.Close()
		}
		// Already running: nudge existing window, no popup.
		return
	}
	defer lockLn.Close()

	var (
		mw              *walk.MainWindow
		ni              *walk.NotifyIcon
		statusLbl       *walk.Label
		verLbl          *walk.Label
		updateLbl       *walk.TextLabel
		logEdit         *walk.TextEdit
		verCombo        *walk.ComboBox
		installBtn      *walk.PushButton
		restartPanelBtn *walk.PushButton
		progressBar     *walk.ProgressBar
		progressLbl     *walk.Label
		busyMu          sync.Mutex
		busy            bool
		showMu          sync.Mutex
		pending         *CheckResult
		metaByLabel     = map[string]VersionMeta{}
		labelOrder      []string
		lastOTALocal    string
		otaSilentMu     sync.Mutex
		otaSilentBusy   bool
		panelPendingExe string
		panelPendingNew string
	)

	clearPanelPending := func() {
		panelPendingExe = ""
		panelPendingNew = ""
		if restartPanelBtn != nil {
			restartPanelBtn.SetVisible(false)
			restartPanelBtn.SetEnabled(false)
		}
	}
	showPanelPending := func(exePath, newPath string) {
		panelPendingExe = exePath
		panelPendingNew = newPath
		if restartPanelBtn != nil {
			restartPanelBtn.SetVisible(true)
			restartPanelBtn.SetEnabled(true)
		}
	}

	resetProgress := func() {
		if progressBar != nil {
			progressBar.SetRange(0, 100)
			progressBar.SetValue(0)
			progressBar.SetVisible(false)
		}
		if progressLbl != nil {
			_ = progressLbl.SetText("")
			progressLbl.SetVisible(false)
		}
	}

	setProgress := func(phase string, current, total int64, message string) {
		if progressBar == nil {
			return
		}
		if !progressBar.Visible() {
			progressBar.SetVisible(true)
		}
		if progressLbl != nil {
			if !progressLbl.Visible() {
				progressLbl.SetVisible(true)
			}
			if message != "" {
				_ = progressLbl.SetText(message)
			}
		}
		progressBar.SetRange(0, 100)
		if phase == "download" && total > 0 {
			pct := int(current * 100 / total)
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			progressBar.SetValue(pct)
			return
		}
		switch phase {
		case "extract":
			progressBar.SetValue(70)
		case "replace":
			progressBar.SetValue(85)
		case "start":
			progressBar.SetValue(95)
		case "done":
			progressBar.SetValue(100)
		default:
			if progressBar.Value() < 5 {
				progressBar.SetValue(5)
			}
		}
	}

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

	setUpdateStatus := func(s string) {
		if updateLbl == nil {
			return
		}
		_ = updateLbl.SetText(strings.TrimSpace(s))
	}
	refreshLogs := func() {
		if logEdit == nil {
			return
		}
		lines := bot.Logs()
		text := "（暂无日志）"
		if len(lines) > 0 {
			if len(lines) > 120 {
				lines = lines[len(lines)-120:]
			}
			text = strings.Join(lines, "\r\n")
		}
		if logEdit.Text() != text {
			logEdit.SetText(text)
			n := len([]rune(text))
			logEdit.SetTextSelection(n, n)
			logEdit.ScrollToCaret()
		}
	}

	selectedMeta := func() (VersionMeta, bool) {
		if verCombo == nil {
			return VersionMeta{}, false
		}
		label := strings.TrimSpace(verCombo.Text())
		meta, ok := metaByLabel[label]
		return meta, ok
	}

	syncInstallButton := func() {
		if installBtn == nil {
			return
		}
		// Keep fixed label so button width never jumps.
		_ = installBtn.SetText("安装选中版本")
		_, ok := selectedMeta()
		if !ok || pending == nil {
			installBtn.SetEnabled(false)
			return
		}
		installBtn.SetEnabled(!isBusy())
	}

	clearVersionPicker := func() {
		pending = nil
		metaByLabel = map[string]VersionMeta{}
		labelOrder = nil
		if verCombo != nil {
			_ = verCombo.SetModel([]string{})
			_ = verCombo.SetText("")
		}
		syncInstallButton()
	}

	populateVersionPicker := func(result *CheckResult) {
		pending = result
		metaByLabel = map[string]VersionMeta{}
		labelOrder = nil
		local := result.Local
		for _, meta := range result.Versions {
			label := FormatVersionLabel(meta, local)
			if _, exists := metaByLabel[label]; exists {
				continue
			}
			metaByLabel[label] = meta
			labelOrder = append(labelOrder, label)
		}
		defaultIdx := 0
		latest := normalizeTag(result.Latest.Version)
		for i, label := range labelOrder {
			if latest != "" && metaByLabel[label].Version == latest {
				defaultIdx = i
				break
			}
		}
		if verCombo != nil {
			_ = verCombo.SetModel(labelOrder)
			if len(labelOrder) > 0 {
				// non-editable ComboBox needs CurrentIndex; SetText alone won't select
				_ = verCombo.SetCurrentIndex(defaultIdx)
			}
		}
		syncInstallButton()
	}

	silentRefreshOTA := func() {
		otaSilentMu.Lock()
		if otaSilentBusy {
			otaSilentMu.Unlock()
			return
		}
		otaSilentBusy = true
		otaSilentMu.Unlock()
		go func() {
			defer func() {
				otaSilentMu.Lock()
				otaSilentBusy = false
				otaSilentMu.Unlock()
			}()
			if isBusy() {
				return
			}
			InvalidateOTACache()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			result, err := bot.CheckOTA(ctx)
			cancel()
			if err != nil || result == nil || !result.OK {
				return
			}
			if mw == nil {
				return
			}
			mw.Synchronize(func() {
				if isBusy() {
					return
				}
				lastOTALocal = normalizeTag(result.Local)
				localV := result.Local
				remoteV := result.Latest.Version
				plat := result.Platform
				if result.HasUpdate {
					setUpdateStatus(fmt.Sprintf(
						"远端最新 %s（当前 %s · %s）。可从下方选择任意历史版本升级或降级；安装将短暂停止服务。",
						remoteV, localV, plat,
					))
				} else {
					setUpdateStatus(fmt.Sprintf(
						"已是最新版本。本地 %s（%s）· 远端 %s。如需回退，可从下方选择历史版本安装。",
						localV, plat, remoteV,
					))
				}
				populateVersionPicker(result)
			})
		}()
	}

	refreshStatus := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		h, err := bot.FetchHealth(ctx)
		text := "● 离线 · 服务不可达"
		display := version.String()
		botVer := ""
		if err == nil {
			ver, _ := h["version"].(string)
			mode, _ := h["mode"].(string)
			off := h["offsetMs"]
			text = fmt.Sprintf("● 运行中 · v%s · %v · 时钟偏移 %vms", ver, mode, off)
			if v := strings.TrimSpace(ver); v != "" {
				display = v
				botVer = v
			}
		}
		if mw != nil {
			mw.Synchronize(func() {
				if statusLbl != nil {
					_ = statusLbl.SetText(text)
				}
				if verLbl != nil {
					_ = verLbl.SetText("面板 " + display)
				}
				_ = mw.SetTitle("Anubisbot 面板 — " + display)
				if ni != nil {
					_ = ni.SetToolTip("Anubisbot 面板 " + display)
				}
				refreshLogs()
			})
		}
		// Keep OTA「当前」 in sync with live bot version (not panel build when offline).
		if botVer == "" {
			return
		}
		live := normalizeTag(botVer)
		if live == "" || live == lastOTALocal {
			return
		}
		lastOTALocal = live
		if pending == nil || normalizeTag(pending.Local) == live {
			return
		}
		if mw != nil {
			mw.Synchronize(func() {
				if pending == nil {
					return
				}
				pending.Local = live
				pending.HasUpdate = versionGT(pending.Latest.Version, live)
				populateVersionPicker(pending)
			})
		}
		silentRefreshOTA()
	}

	runAsync := func(fn func() error, okMsg string) {
		if isBusy() {
			return
		}
		setBusy(true)
		if mw != nil {
			mw.Synchronize(syncInstallButton)
		}
		go func() {
			err := fn()
			setBusy(false)
			if mw != nil {
				mw.Synchronize(func() {
					syncInstallButton()
					if err != nil {
						bot.AppendLog("❌ " + err.Error())
					} else if okMsg != "" {
						bot.AppendLog("✅ " + okMsg)
					}
					refreshStatus()
				})
			}
		}()
	}

	openOps := func() {
		url := strings.TrimRight(store.Get().BotHTTP, "/") + "/"
		if err := OpenBrowser(url); err != nil {
			setUpdateStatus("打开网页失败：" + err.Error())
			bot.AppendLog("❌ 打开网页: " + err.Error())
			return
		}
		bot.AppendLog("opened " + url)
		setUpdateStatus("已打开运维页")
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
			tokenEdit *walk.LineEdit
			cfgHint   *walk.Label
			acceptPB  *walk.PushButton
		)
		tokHint := cfg.GithubToken
		_, err := Dialog{
			AssignTo:      &dlg,
			Title:         "面板配置",
			MinSize:       Size{Width: 440, Height: 300},
			Size:          Size{Width: 480, Height: 320},
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
						Label{Text: "GitHub Token"},
						LineEdit{AssignTo: &tokenEdit, Text: tokHint, PasswordMode: true},
					},
				},
				Label{Text: "Token 可留空：自动用 GH_TOKEN / gh auth token"},
				Label{AssignTo: &cfgHint, Text: ""},
				Composite{
					Layout: HBox{},
					Children: []Widget{
						HSpacer{},
						PushButton{AssignTo: &acceptPB, Text: "保存", OnClicked: func() {
							tok := strings.TrimSpace(tokenEdit.Text())
							if tok == "" {
								tok = "-" // clear stored token; runtime still uses gh/env
							}
							_, err := store.Update(Config{
								GithubRepo:  strings.TrimSpace(repoEdit.Text()),
								AssetName:   strings.TrimSpace(assetEdit.Text()),
								BotHTTP:     strings.TrimSpace(httpEdit.Text()),
								BotExe:      strings.TrimSpace(exeEdit.Text()),
								GithubToken: tok,
							})
							if err != nil {
								if cfgHint != nil {
									_ = cfgHint.SetText("保存失败：" + err.Error())
								}
								bot.AppendLog("❌ 配置: " + err.Error())
								return
							}
							bot.AppendLog("config saved")
							setUpdateStatus("配置已保存")
							dlg.Accept()
						}},
						PushButton{Text: "取消", OnClicked: func() { dlg.Cancel() }},
					},
				},
			},
		}.Run(mw)
		if err != nil {
			setUpdateStatus("打开配置失败：" + err.Error())
			bot.AppendLog("❌ 配置窗口: " + err.Error())
		}
	}

	onCheckUpdateDone := func(result *CheckResult, err error) {
		setBusy(false)
		if err != nil || result == nil || !result.OK {
			msg := "检查失败"
			if err != nil {
				msg = err.Error()
			} else if result != nil && result.Error != "" {
				msg = result.Error
			}
			setUpdateStatus("检查失败：" + msg)
			clearVersionPicker()
			bot.AppendLog("检查更新失败: " + msg)
			return
		}
		localV := result.Local
		remoteV := result.Latest.Version
		plat := result.Platform
		if result.HasUpdate {
			setUpdateStatus(fmt.Sprintf(
				"远端最新 %s（当前 %s · %s）。可从下方选择任意历史版本升级或降级；安装将短暂停止服务。",
				remoteV, localV, plat,
			))
		} else {
			setUpdateStatus(fmt.Sprintf(
				"已是最新版本。本地 %s（%s）· 远端 %s。如需回退，可从下方选择历史版本安装。",
				localV, plat, remoteV,
			))
		}
		lastOTALocal = normalizeTag(localV)
		populateVersionPicker(result)
		bot.AppendLog(fmt.Sprintf("检查更新完成: 本地 %s / 远端 %s", localV, remoteV))
		refreshStatus()
	}

	doCheckUpdate := func() {
		if isBusy() {
			return
		}
		setBusy(true)
		resetProgress()
		clearVersionPicker()
		setUpdateStatus("正在检查更新…")
		bot.AppendLog("检查更新…")
		syncInstallButton()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := bot.CheckOTA(ctx)
			if mw != nil {
				mw.Synchronize(func() { onCheckUpdateDone(result, err) })
			}
		}()
	}

	doRestartPanelToUpdate := func() {
		if isBusy() {
			return
		}
		exePath := panelPendingExe
		newPath := panelPendingNew
		if exePath == "" || newPath == "" {
			setUpdateStatus("没有待应用的面板更新")
			return
		}
		if _, err := os.Stat(newPath); err != nil {
			clearPanelPending()
			setUpdateStatus("面板更新文件已失效，请重新安装该版本")
			bot.AppendLog("面板更新：.new 不存在")
			return
		}
		setBusy(true)
		if restartPanelBtn != nil {
			restartPanelBtn.SetEnabled(false)
		}
		setUpdateStatus("正在重启面板以应用更新…")
		bot.AppendLog("用户确认：重启面板以更新")
		if err := SchedulePanelReplaceAndExit(exePath, newPath); err != nil {
			setBusy(false)
			if restartPanelBtn != nil {
				restartPanelBtn.SetEnabled(true)
			}
			fallback := "重启面板失败：" + err.Error() + "。可手动将 " + filepath.Base(newPath) + " 替换为面板程序后重启。"
			setUpdateStatus(fallback)
			bot.AppendLog("❌ " + fallback)
			refreshLogs()
			return
		}
		go func() {
			time.Sleep(300 * time.Millisecond)
			if mw != nil {
				mw.Synchronize(func() { walk.App().Exit(0) })
			} else {
				os.Exit(0)
			}
		}()
	}

	onApplyDone := func(remoteV string, info *ReleaseInfo, err error) {
		setBusy(false)
		syncInstallButton()
		refreshStatus()
		InvalidateOTACache()
		resetProgress()
		if err != nil {
			msg := err.Error()
			setUpdateStatus("安装失败：" + msg)
			bot.AppendLog("❌ 安装 " + remoteV + ": " + msg)
			refreshLogs()
			return
		}
		tag := remoteV
		if info != nil && info.TagName != "" {
			tag = info.TagName
		}
		msg := "已安装 " + tag + "。服务已重启。"
		if info != nil && info.PanelPendingPath != "" && info.PanelExePath != "" {
			showPanelPending(info.PanelExePath, info.PanelPendingPath)
			msg = "已安装 " + tag + "。服务已重启。面板更新已就绪，可点击「重启面板以更新」应用；也可稍后手动重启。"
		} else {
			clearPanelPending()
		}
		setUpdateStatus(msg)
		bot.AppendLog("✅ " + msg)
		if info != nil && info.Body != "" {
			bot.AppendLog("Release: " + info.Body)
		}
		clearVersionPicker()
		refreshLogs()
	}

	doInstallSelected := func() {
		if isBusy() {
			return
		}
		meta, ok := selectedMeta()
		if !ok || pending == nil {
			setUpdateStatus("请先检查更新并选择目标版本")
			bot.AppendLog("安装：尚未选择版本")
			return
		}
		localV := pending.Local
		remoteV := meta.Version
		var verb string
		switch {
		case remoteV == normalizeTag(localV):
			verb = "重新安装"
		case versionGT(remoteV, localV):
			verb = "升级"
		default:
			verb = "降级"
		}
		setBusy(true)
		syncInstallButton()
		setProgress("download", 0, 0, "准备下载…")
		setUpdateStatus(fmt.Sprintf("正在下载并%s到 %s…", verb, remoteV))
		bot.AppendLog(fmt.Sprintf("开始%s到 %s…", verb, remoteV))
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			info, err := bot.ApplyOTA(ctx, remoteV, func(phase string, current, total int64, message string) {
				if mw == nil {
					return
				}
				mw.Synchronize(func() {
					setProgress(phase, current, total, message)
					if message != "" {
						setUpdateStatus(message)
					}
				})
			})
			if mw != nil {
				mw.Synchronize(func() { onApplyDone(remoteV, info, err) })
			}
		}()
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

	pad := Margins{Left: 8, Top: 6, Right: 8, Bottom: 6}
	// Fixed client size: Walk auto-grows the form when label/combo MinSize jumps
	// after status / OTA text updates; lock size so the window stops jumping.
	winSize := Size{Width: 580, Height: 440}
	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "Anubisbot 面板 — " + version.String(),
		MinSize:  winSize,
		MaxSize:  winSize,
		Size:     winSize,
		Layout:   VBox{Margins: Margins{Left: 10, Top: 8, Right: 10, Bottom: 8}, Spacing: 6},
		Children: []Widget{
			Composite{
				Layout: HBox{Spacing: 6},
				Children: []Widget{
					Label{
						Text: "Anubisbot 控制台",
						Font: Font{Family: "Segoe UI", PointSize: 12, Bold: true},
					},
					Label{
						AssignTo: &verLbl,
						Text:     "面板 " + version.String(),
						Font:     Font{Family: "Segoe UI", PointSize: 9},
					},
					HSpacer{},
					PushButton{Text: "配置…", MinSize: Size{Width: 72}, MaxSize: Size{Width: 72}, OnClicked: openConfig},
				},
			},
			GroupBox{
				Title:  "服务",
				Layout: VBox{Margins: pad, Spacing: 6},
				Children: []Widget{
					Label{
						AssignTo: &statusLbl,
						Text:     "状态检测中…",
						MaxSize:  Size{Width: 520, Height: 20},
					},
					Composite{
						Layout: HBox{Spacing: 6},
						Children: []Widget{
							PushButton{Text: "打开网页", MinSize: Size{Width: 80}, MaxSize: Size{Width: 80}, OnClicked: openOps},
							PushButton{Text: "启动", MinSize: Size{Width: 64}, MaxSize: Size{Width: 64}, OnClicked: func() {
								runAsync(func() error { return bot.Start() }, "服务已启动")
							}},
							PushButton{Text: "停止", MinSize: Size{Width: 64}, MaxSize: Size{Width: 64}, OnClicked: func() {
								runAsync(func() error { return bot.Stop() }, "服务已停止")
							}},
							PushButton{Text: "重启", MinSize: Size{Width: 64}, MaxSize: Size{Width: 64}, OnClicked: func() {
								runAsync(func() error { return bot.Restart() }, "服务已重启")
							}},
							PushButton{Text: "刷新", MinSize: Size{Width: 64}, MaxSize: Size{Width: 64}, OnClicked: func() { go refreshStatus() }},
							HSpacer{},
						},
					},
				},
			},
			GroupBox{
				Title:         "软件更新",
				Layout:        VBox{Margins: pad, Spacing: 6},
				StretchFactor: 1,
				Children: []Widget{
					Composite{
						Layout: HBox{Spacing: 8, Alignment: AlignHNearVNear},
						Children: []Widget{
							TextLabel{
								AssignTo:      &updateLbl,
								Text:          "点击「检查更新」查询版本；可选历史版本升级或降级，不会自动安装。",
								StretchFactor: 1,
								MinSize:       Size{Width: 360, Height: 36},
								MaxSize:       Size{Height: 40},
							},
							PushButton{Text: "检查更新", MinSize: Size{Width: 88, Height: 28}, MaxSize: Size{Width: 88}, OnClicked: doCheckUpdate},
						},
					},
					Composite{
						Layout: HBox{Spacing: 6},
						Children: []Widget{
							Label{Text: "目标版本", MinSize: Size{Width: 56}, MaxSize: Size{Width: 56}},
							ComboBox{
								AssignTo:              &verCombo,
								Editable:              false,
								StretchFactor:         1,
								MinSize:               Size{Width: 260},
								OnCurrentIndexChanged: func() { syncInstallButton() },
							},
							PushButton{
								AssignTo:  &installBtn,
								Text:      "安装选中版本",
								MinSize:   Size{Width: 108},
								MaxSize:   Size{Width: 108},
								Enabled:   false,
								OnClicked: doInstallSelected,
							},
							PushButton{
								AssignTo:           &restartPanelBtn,
								Text:               "重启面板以更新",
								MinSize:            Size{Width: 120},
								MaxSize:            Size{Width: 120},
								Enabled:            false,
								Visible:            false,
								AlwaysConsumeSpace: false,
								OnClicked:          doRestartPanelToUpdate,
							},
						},
					},
					ProgressBar{
						AssignTo:           &progressBar,
						MinSize:            Size{Height: 16},
						MaxValue:           100,
						Visible:            false,
						AlwaysConsumeSpace: false,
					},
					Label{
						AssignTo:           &progressLbl,
						Text:               "",
						MinSize:            Size{Height: 16},
						MaxSize:            Size{Height: 16},
						Visible:            false,
						AlwaysConsumeSpace: false,
					},
					Label{Text: "运行日志", Font: Font{Family: "Segoe UI", PointSize: 9}},
					TextEdit{
						AssignTo:      &logEdit,
						ReadOnly:      true,
						VScroll:       true,
						HScroll:       true,
						Text:          "（暂无日志）",
						MinSize:       Size{Height: 120},
						StretchFactor: 1,
					},
				},
			},
		},
	}.Create()); err != nil {
		panic(err)
	}
	resetProgress()
	_ = mw.SetMinMaxSize(walk.Size{Width: winSize.Width, Height: winSize.Height}, walk.Size{Width: winSize.Width, Height: winSize.Height})
	_ = mw.SetSize(walk.Size{Width: winSize.Width, Height: winSize.Height})
	// Walk may still bump size when layout MinSize changes; pin it back.
	preferred := walk.Size{Width: winSize.Width, Height: winSize.Height}
	var pinning bool
	mw.SizeChanged().Attach(func() {
		if pinning || mw == nil {
			return
		}
		cur := mw.Size()
		if cur.Width == preferred.Width && cur.Height == preferred.Height {
			return
		}
		pinning = true
		_ = mw.SetSize(preferred)
		pinning = false
	})

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = true
		mw.Hide()
	})

	icon := loadAppIcon()
	if icon != nil {
		_ = mw.SetIcon(icon)
	}
	ni, err = walk.NewNotifyIcon(mw)
	if err != nil {
		panic(err)
	}
	if icon != nil {
		if err := ni.SetIcon(icon); err != nil {
			bot.AppendLog("tray icon: " + err.Error())
		}
	} else {
		bot.AppendLog("tray icon: missing")
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
	addTray("检查更新", doCheckUpdate)
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

	// Silent check like akgo: notify only, never auto-install.
	go func() {
		time.Sleep(3 * time.Second)
		if isBusy() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := bot.CheckOTA(ctx)
		if err != nil || result == nil || !result.OK {
			return
		}
		if mw == nil {
			return
		}
		mw.Synchronize(func() {
			localV := result.Local
			if result.HasUpdate {
				remoteV := result.Latest.Version
				setUpdateStatus(fmt.Sprintf(
					"发现新版本 %s（当前 %s）。点击「检查更新」查看并安装；不会自动安装。",
					remoteV, localV,
				))
				bot.AppendLog(fmt.Sprintf("发现更新: %s → %s", localV, remoteV))
			}
		})
	}()

	mw.Show()
	mw.Run()
}
