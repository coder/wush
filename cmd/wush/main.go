package main

import (
	"fmt"
	"os"

	"github.com/coder/serpent"
)

func main() {
	cmd := sendCmd()
	cmd.Children = []*serpent.Command{
		receiveCmd(),
	}
	err := cmd.Invoke().WithOS().Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
