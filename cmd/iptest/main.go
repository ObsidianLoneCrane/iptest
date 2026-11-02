package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/ObsidianLoneCrane/iptest/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	exitCode := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(exitCode)
}
