//go:build js && wasm

package xssh

import (
	"context"
	"os"
)

func ListenWindowSize(ctx context.Context) <-chan os.Signal {
	return nil
}
