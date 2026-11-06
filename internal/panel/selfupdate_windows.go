//go:build windows

package panel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const createNewProcessGroup = 0x00000200

// SchedulePanelReplaceAndExit starts a detached PowerShell helper that waits for
// this process to exit, replaces exePath with newPath, then starts the new panel.
// The caller must exit the process afterward so the single-instance lock is released.
func SchedulePanelReplaceAndExit(exePath, newPath string) error {
	return startPanelReplaceHelper(os.Getpid(), exePath, newPath)
}

func startPanelReplaceHelper(pid int, exePath, newPath string) error {
	exePath = filepath.Clean(exePath)
	newPath = filepath.Clean(newPath)
	if exePath == "" || newPath == "" {
		return fmt.Errorf("empty panel update path")
	}
	if _, err := os.Stat(newPath); err != nil {
		return fmt.Errorf("panel .new missing: %w", err)
	}
	dir := filepath.Dir(exePath)
	oldPath := exePath + ".old"
	logPath := filepath.Join(dir, "panel-update.log")

	psQuote := func(s string) string {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}

	script := strings.Join([]string{
		"$ErrorActionPreference = 'Continue'",
		fmt.Sprintf("$log = %s", psQuote(logPath)),
		fmt.Sprintf("$pidToWait = %s", strconv.Itoa(pid)),
		fmt.Sprintf("$exe = %s", psQuote(exePath)),
		fmt.Sprintf("$new = %s", psQuote(newPath)),
		fmt.Sprintf("$old = %s", psQuote(oldPath)),
		fmt.Sprintf("$workdir = %s", psQuote(dir)),
		"function Log($m) { Add-Content -LiteralPath $log -Value ((Get-Date -Format o) + ' ' + $m) }",
		"Log ('wait pid=' + $pidToWait)",
		"try { Wait-Process -Id $pidToWait -Timeout 120 -ErrorAction SilentlyContinue } catch { Log ('wait err ' + $_) }",
		"Start-Sleep -Milliseconds 800",
		"if (Test-Path -LiteralPath $old) { Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue }",
		"if (Test-Path -LiteralPath $exe) { Move-Item -LiteralPath $exe -Destination $old -Force -ErrorAction SilentlyContinue }",
		"if (Test-Path -LiteralPath $exe) { Remove-Item -LiteralPath $exe -Force -ErrorAction SilentlyContinue }",
		"Move-Item -LiteralPath $new -Destination $exe -Force -ErrorAction SilentlyContinue",
		"if (-not (Test-Path -LiteralPath $exe) -and (Test-Path -LiteralPath $new)) { Copy-Item -LiteralPath $new -Destination $exe -Force }",
		"if (-not (Test-Path -LiteralPath $exe)) { Log 'replace failed'; exit 1 }",
		"Log 'replaced ok'",
		"Start-Process -FilePath $exe -WorkingDirectory $workdir",
		"if (Test-Path -LiteralPath $old) { Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue }",
		"Log 'started'",
	}, "\n")

	ps1 := filepath.Join(dir, "panel-update.ps1")
	if err := os.WriteFile(ps1, []byte(script), 0o644); err != nil {
		return fmt.Errorf("write updater script: %w", err)
	}

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", ps1)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createNewProcessGroup,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start panel updater: %w", err)
	}
	// Do not Wait; helper outlives this process. Detach from Go's wait tracking.
	go func() { _ = cmd.Wait() }()
	return nil
}
