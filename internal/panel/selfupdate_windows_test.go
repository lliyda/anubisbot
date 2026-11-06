//go:build windows

package panel

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSameFilePath(t *testing.T) {
	if !sameFilePath(`C:\a\b.exe`, `C:\a\b.exe`) {
		t.Fatal("expected same")
	}
	if !sameFilePath(`C:\a\b.exe`, `c:\a\b.exe`) {
		t.Fatal("expected case-insensitive same")
	}
	if sameFilePath(`C:\a\b.exe`, `C:\a\c.exe`) {
		t.Fatal("expected different")
	}
}

func TestSchedulePanelReplaceMissingNew(t *testing.T) {
	err := SchedulePanelReplaceAndExit(`C:\no\such\panel.exe`, `C:\no\such\panel.exe.new`)
	if err == nil {
		t.Fatal("expected error for missing .new")
	}
}

func TestStartPanelReplaceHelperReplacesAfterExit(t *testing.T) {
	dir := t.TempDir()
	// Use a tiny real PE so Start-Process can launch it (cmd.exe copy).
	srcCmd, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Skip(err)
	}
	exe := filepath.Join(dir, "fake-panel.exe")
	newPath := exe + ".new"
	oldBytes, err := os.ReadFile(srcCmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, append([]byte{}, oldBytes...), 0o755); err != nil {
		t.Fatal(err)
	}
	// Distinct .new content: copy again (same binary is fine; we check mtime/size swap via marker file).
	marker := filepath.Join(dir, "marker.txt")
	if err := os.WriteFile(newPath, oldBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("pending"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Short-lived process whose PID the helper waits on.
	hold := exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep -Milliseconds 500")
	if err := hold.Start(); err != nil {
		t.Fatal(err)
	}
	pid := hold.Process.Pid

	// Helper that also writes a done marker after replace (extend via env not available);
	// instead verify .new is gone and exe exists after wait.
	if err := startPanelReplaceHelper(pid, exe, newPath); err != nil {
		t.Fatal(err)
	}
	_ = hold.Wait()

	deadline := time.Now().Add(15 * time.Second)
	ok := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			if _, err := os.Stat(exe); err == nil {
				ok = true
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Helper Start-Process launches the replaced exe (cmd copy); kill so TempDir can clean up.
	kill := exec.Command("taskkill", "/F", "/IM", "fake-panel.exe")
	hideConsole(kill)
	_ = kill.Run()
	time.Sleep(300 * time.Millisecond)
	if !ok {
		t.Fatalf("replace did not complete; new still present=%v", fileExists(newPath))
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
