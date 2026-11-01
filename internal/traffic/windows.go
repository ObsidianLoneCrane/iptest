//go:build windows

package traffic

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func readCounters(ctx context.Context) (map[string]counter, error) {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(bounded, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); Get-NetAdapterStatistics | Select-Object Name,ReceivedBytes,SentBytes | ConvertTo-Json -Compress")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	body, err := command.Output()
	if err != nil {
		return nil, errors.Join(errors.New("could not read Windows interface counters"), bounded.Err())
	}
	return parseWindows(body)
}
