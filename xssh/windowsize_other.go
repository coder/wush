//go:build !windows
// +build !windows

package xssh

import (
	"context"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

func ListenWindowSize(ctx context.Context) <-chan os.Signal {
	windowSize := make(chan os.Signal, 1)
	signal.Notify(windowSize, unix.SIGWINCH)
	go func() {
		<-ctx.Done()
		signal.Stop(windowSize)
	}()
	return windowSize
}
