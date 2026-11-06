//go:build !windows

package panel

import "fmt"

// SchedulePanelReplaceAndExit is only implemented on Windows.
func SchedulePanelReplaceAndExit(exePath, newPath string) error {
	return fmt.Errorf("panel self-update not supported on this OS")
}
