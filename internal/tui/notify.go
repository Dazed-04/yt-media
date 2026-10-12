package tui

import (
	"fmt"
	"os/exec"
	"runtime"
)

func notifyBatchComplete(ok, failed int) {
	title := "Downloads complete"
	body := fmt.Sprintf("%d downloaded", ok)
	if failed > 0 {
		body += fmt.Sprintf(", %d failed", failed)
	}
	if runtime.GOOS == "darwin" {
		script := fmt.Sprintf(`display notification "%s" with title "%s"`, body, title)
		_ = exec.Command("osascript", "-e", script).Run()
		return
	}
	_ = exec.Command("notify-send", title, body).Run()
}
