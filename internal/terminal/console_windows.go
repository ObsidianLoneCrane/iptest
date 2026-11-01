package terminal

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func consoleWidth(file *os.File) (int, bool) {
	type coordinate struct{ x, y int16 }
	type rectangle struct{ left, top, right, bottom int16 }
	var info struct {
		size       coordinate
		cursor     coordinate
		attributes uint16
		window     rectangle
		maximum    coordinate
	}
	procedure := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleScreenBufferInfo")
	result, _, _ := procedure.Call(file.Fd(), uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(file)
	if result == 0 {
		return 0, false
	}
	return int(info.window.right-info.window.left) + 1, true
}
