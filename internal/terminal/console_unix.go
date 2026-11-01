//go:build linux || darwin

package terminal

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func consoleWidth(file *os.File) (int, bool) {
	var size struct{ rows, columns, x, y uint16 }
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size)))
	runtime.KeepAlive(file)
	if err != 0 {
		return 0, false
	}
	return int(size.columns), true
}
