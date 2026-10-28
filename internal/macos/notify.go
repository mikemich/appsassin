//go:build darwin

package macos

import (
	"os/exec"
	"strings"
)

// Notify posts a Notification Center banner. It goes through osascript, so
// macOS shows it as coming from Script Editor; if that app's notifications
// are turned off in System Settings, nothing appears.
func Notify(title, subtitle, body string) error {
	q := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	script := "display notification " + q(body) + " with title " + q(title)
	if subtitle != "" {
		script += " subtitle " + q(subtitle)
	}
	return exec.Command("osascript", "-e", script).Run()
}
