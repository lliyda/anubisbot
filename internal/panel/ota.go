package panel

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ReleaseInfo struct {
	TagName    string `json:"tagName"`
	Name       string `json:"name"`
	AssetName  string `json:"assetName"`
	AssetURL   string `json:"assetURL"`
	Current    string `json:"current"`
	Update     bool   `json:"update"`
	Body       string `json:"body,omitempty"`
	Published  string `json:"publishedAt,omitempty"`
}

type ghRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func normalizeTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

func (m *BotManager) CheckOTA(ctx context.Context) (*ReleaseInfo, error) {
	cfg := m.cfg.Get()
	repo := strings.TrimSpace(cfg.GithubRepo)
	if repo == "" || repo == "OWNER/REPO" || !strings.Contains(repo, "/") {
		return nil, fmt.Errorf("请先在配置中填写有效的 GitHub 仓库 owner/repo")
	}
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "anubisbot-panel")
	cli := &http.Client{Timeout: 20 * time.Second}
	res, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("github %s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	var rel ghRelease
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		return nil, err
	}
	want := strings.TrimSpace(cfg.AssetName)
	var assetName, assetURL string
	for _, a := range rel.Assets {
		n := a.Name
		if want != "" && strings.EqualFold(n, want) {
			assetName, assetURL = n, a.BrowserDownloadURL
			break
		}
	}
	if assetURL == "" {
		for _, a := range rel.Assets {
			ln := strings.ToLower(a.Name)
			if strings.Contains(ln, "windows") && (strings.HasSuffix(ln, ".zip") || strings.Contains(ln, "amd64")) {
				assetName, assetURL = a.Name, a.BrowserDownloadURL
				break
			}
		}
	}
	if assetURL == "" && len(rel.Assets) > 0 {
		assetName, assetURL = rel.Assets[0].Name, rel.Assets[0].BrowserDownloadURL
	}
	if assetURL == "" {
		return nil, fmt.Errorf("release %s 没有可下载资产", rel.TagName)
	}

	current := "dev"
	if h, err := m.FetchHealth(ctx); err == nil {
		if v, ok := h["version"].(string); ok && v != "" {
			current = v
		}
	}
	tag := normalizeTag(rel.TagName)
	info := &ReleaseInfo{
		TagName:   rel.TagName,
		Name:      rel.Name,
		AssetName: assetName,
		AssetURL:  assetURL,
		Current:   current,
		Update:    tag != "" && tag != normalizeTag(current),
		Body:      rel.Body,
		Published: rel.PublishedAt,
	}
	return info, nil
}

func (m *BotManager) ApplyOTA(ctx context.Context) (*ReleaseInfo, error) {
	info, err := m.CheckOTA(ctx)
	if err != nil {
		return nil, err
	}
	if !info.Update && normalizeTag(info.Current) != "dev" {
		m.AppendLog("OTA: already up to date " + info.Current)
		return info, fmt.Errorf("已是最新版本 %s", info.Current)
	}
	m.AppendLog("OTA: downloading " + info.AssetName)
	zipPath, err := m.downloadAsset(ctx, info.AssetURL, info.AssetName)
	if err != nil {
		return info, err
	}
	defer os.Remove(zipPath)

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
		panelDst := filepath.Join(m.cfg.Root(), panelName)
		// On Windows cannot replace running panel easily; write .new for next manual restart
		newPath := panelDst + ".new"
		if err := copyFile(panelSrc, newPath); err == nil {
			m.AppendLog("OTA: wrote " + filepath.Base(newPath) + "（面板需手动替换重启）")
		}
	}

	m.AppendLog("OTA: starting bot")
	if err := m.Start(); err != nil {
		return info, err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if m.Healthy(context.Background()) {
			m.AppendLog("OTA: done -> " + info.TagName)
			return info, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return info, fmt.Errorf("升级后健康检查超时")
}

func (m *BotManager) downloadAsset(ctx context.Context, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "anubisbot-panel")
	cli := &http.Client{Timeout: 5 * time.Minute}
	res, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("download %s", res.Status)
	}
	f, err := os.CreateTemp(m.cfg.Root(), "ota-dl-*.zip")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, res.Body); err != nil {
		os.Remove(f.Name())
		return "", err
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
			// fallback copy+remove
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
