package proxy

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"
)

func hideDiscoveryWindow(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func readWindowsSettings() (windowsSettings, error) {
	winhttp := syscall.NewLazyDLL("winhttp.dll")
	get := winhttp.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	free := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalFree")
	var config struct {
		AutoDetect            int32
		Script, Proxy, Bypass *uint16
	}
	success, _, callErr := get.Call(uintptr(unsafe.Pointer(&config)))
	if success == 0 {
		if errors.Is(callErr, syscall.ERROR_FILE_NOT_FOUND) {
			return windowsSettings{}, nil
		}
		return windowsSettings{}, ErrDiscovery
	}
	for _, pointer := range []*uint16{config.Script, config.Proxy, config.Bypass} {
		if pointer != nil {
			defer free.Call(uintptr(unsafe.Pointer(pointer)))
		}
	}
	return windowsSettings{AutoDetect: config.AutoDetect != 0, Script: windowsString(config.Script), Proxy: windowsString(config.Proxy), Bypass: windowsString(config.Bypass)}, nil
}

func windowsString(pointer *uint16) string {
	if pointer == nil {
		return ""
	}
	var value []uint16
	for offset := uintptr(0); offset < 64*1024; offset++ {
		character := *(*uint16)(unsafe.Add(unsafe.Pointer(pointer), offset*2))
		if character == 0 {
			break
		}
		value = append(value, character)
	}
	return syscall.UTF16ToString(value)
}
