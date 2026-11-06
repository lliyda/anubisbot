package panel

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	otaCacheMu   sync.Mutex
	otaCacheAt   time.Time
	otaCacheKey  string
	otaCacheData *CheckResult
)

func InvalidateOTACache() {
	otaCacheMu.Lock()
	otaCacheData = nil
	otaCacheKey = ""
	otaCacheAt = time.Time{}
	otaCacheMu.Unlock()
}

// VersionMeta is one installable GitHub Release (akgo-style picker entry).
type VersionMeta struct {
	Version     string `json:"version"`
	TagName     string `json:"tagName"`
	Name        string `json:"name"`
	Notes       string `json:"notes,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
	AssetName   string `json:"assetName"`
	AssetURL    string `json:"assetURL"`
}

// CheckResult mirrors akgo check_update(): list versions, never auto-install.
type CheckResult struct {
	OK        bool          `json:"ok"`
	Error     string        `json:"error,omitempty"`
	HasUpdate bool          `json:"hasUpdate"`
	Local     string        `json:"local"`
	Platform  string        `json:"platform"`
	Latest    VersionMeta   `json:"latest"`
	Versions  []VersionMeta `json:"versions"`
}

// ReleaseInfo is kept for install progress / logs.
type ReleaseInfo struct {
	TagName          string `json:"tagName"`
	Name             string `json:"name"`
	AssetName        string `json:"assetName"`
	AssetURL         string `json:"assetURL"`
	Current          string `json:"current"`
	Update           bool   `json:"update"`
	Body             string `json:"body,omitempty"`
	Published        string `json:"publishedAt,omitempty"`
	PanelPendingPath string `json:"panelPendingPath,omitempty"` // .new next to running panel; UI restarts to apply
	PanelExePath     string `json:"panelExePath,omitempty"`     // running panel exe to replace
}

type ghRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func normalizeTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

func versionParts(v string) []int {
	v = normalizeTag(v)
	if v == "" || strings.EqualFold(v, "dev") {
		return []int{0}
	}
	parts := strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == '+'
	})
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				break
			}
			n = n*10 + int(ch-'0')
		}
		// keep numeric prefix only; non-numeric segments become 0
		if p != "" && (p[0] < '0' || p[0] > '9') {
			out = append(out, 0)
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return []int{0}
	}
	return out
}

// versionGT reports whether remote is strictly newer than local (akgo version_gt).
func versionGT(remote, local string) bool {
	a, b := versionParts(remote), versionParts(local)
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		ai, bi := 0, 0
		if i < len(a) {
			ai = a[i]
		}
		if i < len(b) {
			bi = b[i]
		}
		if ai > bi {
			return true
		}
		if ai < bi {
			return false
		}
	}
	return false
}

func pickAsset(rel ghRelease, want string) (name, url string) {
	for _, a := range rel.Assets {
		if want != "" && strings.EqualFold(a.Name, want) {
			return a.Name, a.BrowserDownloadURL
		}
	}
	for _, a := range rel.Assets {
		ln := strings.ToLower(a.Name)
		if strings.Contains(ln, "windows") && (strings.HasSuffix(ln, ".zip") || strings.Contains(ln, "amd64")) {
			return a.Name, a.BrowserDownloadURL
		}
	}
	if len(rel.Assets) > 0 {
		return rel.Assets[0].Name, rel.Assets[0].BrowserDownloadURL
	}
	return "", ""
}

func (m *BotManager) localVersion(ctx context.Context) string {
	if h, err := m.FetchHealth(ctx); err == nil {
		if v, ok := h["version"].(string); ok && strings.TrimSpace(v) != "" {
			return normalizeTag(v)
		}
	}
	return "dev"
}

func resolveGithubToken(cfg Config) string {
	if t := strings.TrimSpace(cfg.GithubToken); t != "" && t != "-" {
		return t
	}
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(k)); t != "" {
			return t
		}
	}
	// Reuse `gh auth login` session when available.
	cmd := exec.Command("gh", "auth", "token")
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (m *BotManager) githubGET(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "anubisbot-panel")
	if tok := resolveGithubToken(m.cfg.Get()); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	cli := &http.Client{Timeout: 30 * time.Second}
	return cli.Do(req)
}

func (m *BotManager) repoConfigured() (string, string, error) {
	cfg := m.cfg.Get()
	repo := strings.TrimSpace(cfg.GithubRepo)
	if repo == "" || repo == "OWNER/REPO" || !strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("请先在配置中填写有效的 GitHub 仓库 owner/repo")
	}
	return repo, strings.TrimSpace(cfg.AssetName), nil
}

func metaFromRelease(rel ghRelease, wantAsset string) (VersionMeta, bool) {
	assetName, assetURL := pickAsset(rel, wantAsset)
	if assetURL == "" || rel.Draft {
		return VersionMeta{}, false
	}
	ver := normalizeTag(rel.TagName)
	if ver == "" {
		return VersionMeta{}, false
	}
	return VersionMeta{
		Version:     ver,
		TagName:     rel.TagName,
		Name:        rel.Name,
		Notes:       strings.TrimSpace(rel.Body),
		PublishedAt: rel.PublishedAt,
		AssetName:   assetName,
		AssetURL:    assetURL,
	}, true
}

// CheckOTA lists GitHub releases (akgo check_update style). Does not install.
func (m *BotManager) CheckOTA(ctx context.Context) (*CheckResult, error) {
	repo, wantAsset, err := m.repoConfigured()
	if err != nil {
		return &CheckResult{OK: false, Error: err.Error(), Local: m.localVersion(ctx), Platform: "windows-amd64"}, err
	}
	local := m.localVersion(ctx)
	cacheKey := repo + "|" + wantAsset + "|" + local
	otaCacheMu.Lock()
	if otaCacheData != nil && otaCacheKey == cacheKey && time.Since(otaCacheAt) < 2*time.Minute {
		cp := *otaCacheData
		cp.Local = local
		cp.HasUpdate = versionGT(cp.Latest.Version, local)
		otaCacheMu.Unlock()
		return &cp, nil
	}
	otaCacheMu.Unlock()

	url := "https://api.github.com/repos/" + repo + "/releases?per_page=30"
	res, err := m.githubGET(ctx, url)
	if err != nil {
		return &CheckResult{OK: false, Error: err.Error(), Local: local, Platform: "windows-amd64"}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		raw := strings.TrimSpace(string(b))
		msg := fmt.Sprintf("github %s: %s", res.Status, raw)
		if res.StatusCode == 403 && strings.Contains(strings.ToLower(raw), "rate limit") {
			msg = "GitHub API 速率受限。请先执行 gh auth login，或在配置里填写 Github Token 后重试。"
		}
		return &CheckResult{OK: false, Error: msg, Local: local, Platform: "windows-amd64"}, fmt.Errorf("%s", msg)
	}
	var rels []ghRelease
	if err := json.NewDecoder(res.Body).Decode(&rels); err != nil {
		return &CheckResult{OK: false, Error: err.Error(), Local: local, Platform: "windows-amd64"}, err
	}

	versions := make([]VersionMeta, 0, len(rels))
	seen := map[string]bool{}
	for _, rel := range rels {
		meta, ok := metaFromRelease(rel, wantAsset)
		if !ok || seen[meta.Version] {
			continue
		}
		seen[meta.Version] = true
		versions = append(versions, meta)
	}
	if len(versions) == 0 {
		msg := "仓库没有可下载的 Release 资产（需要 anubisbot-windows-amd64.zip）"
		return &CheckResult{OK: false, Error: msg, Local: local, Platform: "windows-amd64"}, fmt.Errorf("%s", msg)
	}

	latest := versions[0]
	out := &CheckResult{
		OK:        true,
		HasUpdate: versionGT(latest.Version, local),
		Local:     local,
		Platform:  "windows-amd64",
		Latest:    latest,
		Versions:  versions,
	}
	otaCacheMu.Lock()
	otaCacheAt = time.Now()
	otaCacheKey = cacheKey
	cp := *out
	otaCacheData = &cp
	otaCacheMu.Unlock()
	return out, nil
}

// FormatVersionLabel builds akgo-style combobox labels.
func FormatVersionLabel(meta VersionMeta, local string) string {
	ver := meta.Version
	var tag string
	switch {
	case ver == normalizeTag(local):
		tag = "当前"
	case versionGT(ver, local):
		tag = "可升级"
	default:
		tag = "可降级"
	}
	pub := meta.PublishedAt
	if len(pub) >= 10 {
		pub = pub[:10]
	}
	if pub != "" {
		return fmt.Sprintf("%s（%s）· %s", ver, tag, pub)
	}
	return fmt.Sprintf("%s（%s）", ver, tag)
}

// ProgressFunc reports OTA progress (akgo-style phases).
// phase: download | extract | replace | start
// current/total are bytes for download; otherwise current=0 total=0.
type ProgressFunc func(phase string, current, total int64, message string)

func reportProgress(fn ProgressFunc, phase string, current, total int64, message string) {
	if fn != nil {
		fn(phase, current, total, message)
	}
}

// ApplyOTA downloads and installs the selected version (upgrade / downgrade / reinstall).
func (m *BotManager) ApplyOTA(ctx context.Context, targetVersion string, onProgress ProgressFunc) (*ReleaseInfo, error) {
	checked, err := m.CheckOTA(ctx)
	if err != nil {
		return nil, err
	}
	target := normalizeTag(targetVersion)
	if target == "" {
		target = checked.Latest.Version
	}
	var meta VersionMeta
	for _, v := range checked.Versions {
		if v.Version == target {
			meta = v
			break
		}
	}
	if meta.AssetURL == "" {
		return nil, fmt.Errorf("未知版本 %s", targetVersion)
	}

	info := &ReleaseInfo{
		TagName:   meta.TagName,
		Name:      meta.Name,
		AssetName: meta.AssetName,
		AssetURL:  meta.AssetURL,
		Current:   checked.Local,
		Update:    versionGT(meta.Version, checked.Local),
		Body:      meta.Notes,
		Published: meta.PublishedAt,
	}

	m.AppendLog("OTA: downloading " + meta.AssetName + " (" + meta.Version + ")")
	reportProgress(onProgress, "download", 0, 0, "开始下载 "+meta.AssetName)
	zipPath, err := m.downloadAsset(ctx, meta.AssetURL, meta.AssetName, onProgress)
	if err != nil {
		return info, err
	}
	defer os.Remove(zipPath)

	reportProgress(onProgress, "extract", 0, 0, "正在解压…")
	tmpDir, err := os.MkdirTemp(m.cfg.Root(), "ota-*")
	if err != nil {
		return info, err
	}
	defer os.RemoveAll(tmpDir)

	botSrc, panelSrc, err := extractExes(zipPath, tmpDir)
	if err != nil {
		return info, err
	}
	if botSrc == "" {
		return info, fmt.Errorf("zip 中未找到 anubisbot.exe")
	}

	reportProgress(onProgress, "replace", 0, 0, "停止服务并替换文件…")
	m.AppendLog("OTA: stopping bot")
	_ = m.Stop()
	time.Sleep(400 * time.Millisecond)

	botDst := m.cfg.BotExePath()
	if err := replaceFile(botSrc, botDst); err != nil {
		return info, fmt.Errorf("替换 bot 失败: %w", err)
	}
	m.AppendLog("OTA: replaced " + filepath.Base(botDst))

	if panelSrc != "" {
		panelName := m.cfg.Get().PanelExe
		if panelName == "" {
			panelName = "anubisbot-panel.exe"
		}
		rootPanel := filepath.Join(m.cfg.Root(), panelName)
		exePath := rootPanel
		if self, err := os.Executable(); err == nil {
			if resolved, err2 := filepath.EvalSymlinks(self); err2 == nil {
				self = resolved
			}
			exePath = self
		}
		info.PanelExePath = exePath

		// Prefer .new next to the running panel binary; also mirror beside Root panel if different.
		pendingPaths := []string{exePath + ".new"}
		rootNew := rootPanel + ".new"
		if !sameFilePath(rootNew, exePath+".new") {
			pendingPaths = append(pendingPaths, rootNew)
		}

		var wrotePrimary string
		for _, newPath := range pendingPaths {
			if err := copyFile(panelSrc, newPath); err != nil {
				m.AppendLog("OTA: write " + filepath.Base(newPath) + " failed: " + err.Error())
				continue
			}
			m.AppendLog("OTA: wrote " + newPath + "（可点「重启面板以更新」）")
			if wrotePrimary == "" {
				wrotePrimary = newPath
			}
		}
		info.PanelPendingPath = wrotePrimary

		binNew := filepath.Join(m.cfg.Root(), "bin", panelName+".new")
		_ = os.MkdirAll(filepath.Dir(binNew), 0o755)
		_ = copyFile(panelSrc, binNew)
	}

	reportProgress(onProgress, "start", 0, 0, "启动新版本…")
	m.AppendLog("OTA: starting bot")
	if err := m.Start(); err != nil {
		return info, err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if m.Healthy(context.Background()) {
			m.AppendLog("OTA: done -> " + info.TagName)
			reportProgress(onProgress, "done", 0, 0, "安装完成")
			return info, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return info, fmt.Errorf("升级后健康检查超时")
}

type progressReader struct {
	r          io.Reader
	read       int64
	total      int64
	onProgress ProgressFunc
	lastReport time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.read += int64(n)
		now := time.Now()
		if p.onProgress != nil && (now.Sub(p.lastReport) >= 120*time.Millisecond || err == io.EOF || (p.total > 0 && p.read >= p.total)) {
			p.lastReport = now
			msg := "下载中…"
			if p.total > 0 {
				msg = fmt.Sprintf("下载中 %.1f / %.1f MB", float64(p.read)/(1024*1024), float64(p.total)/(1024*1024))
			} else {
				msg = fmt.Sprintf("下载中 %.1f MB", float64(p.read)/(1024*1024))
			}
			p.onProgress("download", p.read, p.total, msg)
		}
	}
	return n, err
}

const otaDownloadNetworkHint = "下载失败：网络异常，无法从 GitHub 获取更新包。请检查网络或代理后重试；仍失败可到 GitHub Release 页手动下载 zip 覆盖安装。"

// friendlyDownloadErr maps raw HTTP/transport errors to short Chinese hints (no giant CDN URLs).
func friendlyDownloadErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	var netErr net.Error
	if errors.As(err, &netErr) {
		return errors.New(otaDownloadNetworkHint)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errors.New(otaDownloadNetworkHint)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errors.New(otaDownloadNetworkHint)
	}
	networkHints := []string{
		"get \"", "timeout", "timed out", "connection reset", "connection refused",
		"no such host", "network is unreachable", "i/o timeout", "tls:", "x509:",
		"wsarecv", "wsasend", "broken pipe", "release-assets", "githubusercontent",
		"use of closed network connection", "forcibly closed",
	}
	for _, h := range networkHints {
		if strings.Contains(lower, h) {
			return errors.New(otaDownloadNetworkHint)
		}
	}
	// Strip accidental long URLs from leftover messages.
	if i := strings.Index(msg, "https://"); i >= 0 {
		end := i
		for end < len(msg) && msg[end] != ' ' && msg[end] != '"' {
			end++
		}
		msg = strings.TrimSpace(msg[:i] + msg[end:])
		msg = strings.Trim(msg, ": \"")
		if msg == "" || strings.EqualFold(msg, "get") {
			return errors.New(otaDownloadNetworkHint)
		}
	}
	return errors.New(msg)
}

func (m *BotManager) downloadAsset(ctx context.Context, url, name string, onProgress ProgressFunc) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", friendlyDownloadErr(err)
	}
	req.Header.Set("User-Agent", "anubisbot-panel")
	if tok := resolveGithubToken(m.cfg.Get()); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	cli := &http.Client{Timeout: 5 * time.Minute}
	res, err := cli.Do(req)
	if err != nil {
		return "", friendlyDownloadErr(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("下载失败：服务器返回 %s。请稍后重试，或手动下载 Release 包。", res.Status)
	}
	f, err := os.CreateTemp(m.cfg.Root(), "ota-dl-*.zip")
	if err != nil {
		return "", err
	}
	defer f.Close()
	pr := &progressReader{r: res.Body, total: res.ContentLength, onProgress: onProgress}
	if _, err := io.Copy(f, pr); err != nil {
		os.Remove(f.Name())
		return "", friendlyDownloadErr(err)
	}
	if onProgress != nil {
		onProgress("download", pr.read, pr.total, "下载完成")
	}
	_ = name
	return f.Name(), nil
}

func extractExes(zipPath, destDir string) (botPath, panelPath string, err error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", "", err
	}
	defer r.Close()
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		lower := strings.ToLower(base)
		if f.FileInfo().IsDir() {
			continue
		}
		if lower != "anubisbot.exe" && lower != "anubisbot-panel.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", "", err
		}
		outPath := filepath.Join(destDir, base)
		out, err := os.Create(outPath)
		if err != nil {
			rc.Close()
			return "", "", err
		}
		_, copyErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if copyErr != nil {
			return "", "", copyErr
		}
		if lower == "anubisbot.exe" {
			botPath = outPath
		} else {
			panelPath = outPath
		}
	}
	return botPath, panelPath, nil
}

func replaceFile(src, dst string) error {
	bak := dst + ".bak"
	_ = os.Remove(bak)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, bak); err != nil {
			if err2 := copyFile(dst, bak); err2 != nil {
				return err
			}
			_ = os.Remove(dst)
		}
	}
	if err := copyFile(src, dst); err != nil {
		_ = os.Rename(bak, dst)
		return err
	}
	return nil
}

func sameFilePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if strings.EqualFold(a, b) {
		return true
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(aa, bb)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
