package tui

import (
	"fmt"
	"os"
	"os/exec"
)

// kittyRemoteControlAvailable reports whether this program can issue
// `kitty @` commands to its own window (requires allow_remote_control
// in the user's kitty.conf — see README).
func kittyRemoteControlAvailable() bool {
	return kittySupported() && os.Getenv("KITTY_LISTEN_ON") != ""
}

// shrinkKittyFont decreases the active OS window's font size by delta
// points. Using --increment means we never need to know or restore an
// absolute value — reversing is just applying -delta later.
func shrinkKittyFont(delta float64) {
	if !kittyRemoteControlAvailable() || delta <= 0 {
		return
	}
	exec.Command("kitty", "@", "set-font-size", "--increment",
		fmt.Sprintf("-%g", delta)).Run()
}

// restoreKittyFont undoes shrinkKittyFont. Safe to call even if the
// shrink never happened (no-ops the same way).
func restoreKittyFont(delta float64) {
	if !kittyRemoteControlAvailable() || delta <= 0 {
		return
	}
	exec.Command("kitty", "@", "set-font-size", "--increment",
		fmt.Sprintf("+%g", delta)).Run()
}
