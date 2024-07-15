package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/serpent"
	"github.com/google/uuid"
	"golang.org/x/xerrors"
	"tailscale.com/ipn/store"
	"tailscale.com/tsnet"
)

func logF(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

func receiveCmd() *serpent.Command {
	var (
	// bindAddr string
	)
	return &serpent.Command{
		Use: "receive",
		Handler: func(inv *serpent.Invocation) error {
			authID := uuid.New()
			ts, err := newTSNet("receive", authID.String())
			if err != nil {
				return err
			}

			fmt.Println("Your Auth ID is:", authID.String())

			ls, err := ts.Listen("tcp", "100.1.1.1:4444")
			if err != nil {
				return err
			}

			wait := make(chan struct{})

			go http.Serve(ls, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(wait)

				fiName := strings.TrimPrefix(r.URL.Path, "/")
				defer r.Body.Close()

				fi, err := os.OpenFile(fiName, os.O_CREATE|os.O_RDWR, 0644)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				_, _ = io.Copy(fi, r.Body)
				fi.Close()

				w.WriteHeader(http.StatusOK)
				w.Write([]byte(fmt.Sprintf("file %q written", fiName)))
				fmt.Printf("Received file %s from %s\n", fiName, r.RemoteAddr)
			}))

			<-wait
			time.Sleep(1 * time.Second)

			// lc, _ := ts.LocalClient()
			// status, err := lc.Status(inv.Context())
			// if err != nil {
			// 	return err
			// }
			// spew.Dump(status)
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

func newTSNet(direction string, authkey string) (*tsnet.Server, error) {
	var err error
	tmp := os.TempDir()
	srv := new(tsnet.Server)
	srv.Dir = tmp
	srv.Hostname = "wgsh-test-" + direction
	srv.Ephemeral = true
	srv.AuthKey = direction + "-" + authkey
	srv.ControlURL = "http://localhost:8080"
	srv.Store, err = store.New(logF, "mem:lol")
	// srv.Logf = logF

	if err != nil {
		return nil, xerrors.Errorf("create state store: %w", err)
	}

	srv.Start()

	// lc, _ := srv.LocalClient()
	// lc.Status()
	// srv.TailscaleIPs()
	return srv, nil
}
