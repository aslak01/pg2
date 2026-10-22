package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// copyToClipboard sets the clipboard both locally (pbcopy) and through the
// terminal via OSC 52, so it also works over SSH. It must run after the TUI
// has released the terminal.
func copyToClipboard(s string) error {
	osc52Err := writeOSC52(s)
	if os.Getenv("SSH_TTY") != "" {
		// pbcopy would only reach the remote host's pasteboard.
		return osc52Err
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pbcopy: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func writeOSC52(s string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("osc52: %w", err)
	}
	defer tty.Close()
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\x07"
	if os.Getenv("TMUX") != "" {
		// tmux passthrough (needs `set -g allow-passthrough on`).
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	_, err = tty.WriteString(seq)
	return err
}
