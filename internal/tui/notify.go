package tui

import (
	"fmt"
	"os/exec"
	"runtime"
)

func notifyBatchComplete(total int) {
	title := "Downloads complete"
	body := fmt.Sprintf("%d items finished downloading", total)
	if runtime.GOOS == "darwin" {
		script := fmt.Sprintf(`display notification "%s" with title "%s"`, body, title)
		_ = exec.Command("osascript", "-e", script).Run()
		return
	}
	_ = exec.Command("notify-send", title, body).Run()
}
