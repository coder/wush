package main

import (
	"fmt"
	"net/http/httputil"
	"os"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/coder/serpent"
)

func sendCmd() *serpent.Command {
	var (
	// bindAddr string
	)
	return &serpent.Command{
		Use: "send",
		Handler: func(inv *serpent.Invocation) error {
			var authID string
			huh.NewInput().
				Title("Enter your Auth ID:").
				Value(&authID).
				Run()

			ts, err := newTSNet("send", authID)
			if err != nil {
				return err
			}

			time.Sleep(5 * time.Second)

			fi, err := os.Open(inv.Args[0])
			if err != nil {
				return err
			}

			hc := ts.HTTPClient()
			res, err := hc.Post("http://100.1.1.1:4444/"+inv.Args[0], "text/plain", fi)
			if err != nil {
				return err
			}
			defer res.Body.Close()

			out, err := httputil.DumpResponse(res, true)
			if err != nil {
				return err
			}
			fmt.Println(string(out))
			return nil
		},
		Options: []serpent.Option{
			// {
			// 	Flag:    "bind",
			// 	Default: "localhost:8080",
			// 	Value:   serpent.StringOf(&bindAddr),
			// },
		},
	}
}
