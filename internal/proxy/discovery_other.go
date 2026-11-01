//go:build !windows

package proxy

import "os/exec"

func hideDiscoveryWindow(command *exec.Cmd)         {}
func readWindowsSettings() (windowsSettings, error) { return windowsSettings{}, ErrDiscovery }
