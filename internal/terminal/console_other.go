//go:build !windows && !linux && !darwin

package terminal

import "os"

func consoleWidth(*os.File) (int, bool) { return 0, false }
