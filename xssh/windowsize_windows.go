//go:build windows
// +build windows

package xssh

import (
	"context"
	"os"
	"time"
)

func ListenWindowSize(ctx context.Context) <-chan os.Signal {
	windowSize := make(chan os.Signal, 3)
	ticker := time.NewTicker(time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			windowSize <- nil
		}
	}()
	return windowSize
}
