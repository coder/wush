package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/coder/wush/overlay"
	"github.com/coder/wush/tsserver"
	"github.com/schollz/progressbar/v3"
	"tailscale.com/net/netns"
)

func cpCmd() *serpent.Command {
	var (
		authID             string
		waitP2P            bool
		stunAddrOverride   string
		stunAddrOverrideIP netip.Addr
	)
	return &serpent.Command{
		Use:   "cp <file>",
		Short: "Transfer files.",
		Long:  "Transfer files to a " + cliui.Code("wush") + " peer. ",
		Middleware: serpent.Chain(
			serpent.RequireNArgs(1),
		),
		Handler: func(inv *serpent.Invocation) error {
			ctx := inv.Context()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			logF := func(str string, args ...any) {
				fmt.Fprintf(inv.Stderr, str+"\n", args...)
			}

			if authID == "" {
				err := huh.NewInput().
					Title("Enter your Auth ID:").
					Value(&authID).
					Run()
				if err != nil {
					return fmt.Errorf("get auth id: %w", err)
				}
			}

			dm, err := tsserver.DERPMapTailscale(ctx)
			if err != nil {
				return err
			}

			if stunAddrOverride != "" {
				stunAddrOverrideIP, err = netip.ParseAddr(stunAddrOverride)
				if err != nil {
					return fmt.Errorf("parse stun addr override: %w", err)
				}
			}

			send := overlay.NewSendOverlay(logger, dm)
			send.STUNIPOverride = stunAddrOverrideIP

			err = send.Auth.Parse(authID)
			if err != nil {
				return fmt.Errorf("parse auth key: %w", err)
			}

			logF("Auth information:")
			stunStr := send.Auth.ReceiverStunAddr.String()
			if !send.Auth.ReceiverStunAddr.IsValid() {
				stunStr = "Disabled"
			}
			logF("\t> Server overlay STUN address: %s", cliui.Code(stunStr))
			derpStr := "Disabled"
			if send.Auth.ReceiverDERPRegionID > 0 {
				derpStr = dm.Regions[int(send.Auth.ReceiverDERPRegionID)].RegionName
			}
			logF("\t> Server overlay DERP home:    %s", cliui.Code(derpStr))
			logF("\t> Server overlay public key:   %s", cliui.Code(send.Auth.ReceiverPublicKey.ShortString()))
			logF("\t> Server overlay auth key:     %s", cliui.Code(send.Auth.OverlayPrivateKey.Public().ShortString()))

			s, err := tsserver.NewServer(ctx, logger, send)
			if err != nil {
				return err
			}

			if send.Auth.ReceiverDERPRegionID != 0 {
				go send.ListenOverlayDERP(ctx)
			} else if send.Auth.ReceiverStunAddr.IsValid() {
				go send.ListenOverlaySTUN(ctx)
			} else {
				return errors.New("auth key provided neither DERP nor STUN")
			}

			go s.ListenAndServe(ctx)
			netns.SetDialerOverride(s.Dialer())
			ts, err := newTSNet("send")
			if err != nil {
				return err
			}
			ts.Logf = func(string, ...any) {}
			ts.UserLogf = func(string, ...any) {}

			logF("Bringing Wireguard up..")
			ts.Up(ctx)
			logF("Wireguard is ready!")

			lc, err := ts.LocalClient()
			if err != nil {
				return err
			}

			ip, err := waitUntilHasPeerHasIP(ctx, logF, lc)
			if err != nil {
				return err
			}

			if waitP2P {
				err := waitUntilHasP2P(ctx, logF, lc)
				if err != nil {
					return err
				}
			}

			fiPath := inv.Args[0]
			fiName := filepath.Base(inv.Args[0])

			fi, err := os.Open(fiPath)
			if err != nil {
				return err
			}
			defer fi.Close()

			fiStat, err := fi.Stat()
			if err != nil {
				return err
			}

			bar := progressbar.DefaultBytes(
				fiStat.Size(),
				fmt.Sprintf("Uploading %q", fiPath),
			)
			barReader := progressbar.NewReader(fi, bar)

			hc := ts.HTTPClient()
			req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s:4444/%s", ip.String(), fiName), &barReader)
			if err != nil {
				return err
			}
			req.ContentLength = fiStat.Size()

			res, err := hc.Do(req)
			if err != nil {
				return err
			}
			defer res.Body.Close()

			out, err := httputil.DumpResponse(res, true)
			if err != nil {
				return err
			}
			bar.Close()
			fmt.Println(string(out))

			return nil
		},
		Options: []serpent.Option{
			{
				Flag:        "auth-id",
				Env:         "WUSH_AUTH_ID",
				Description: "The auth id returned by " + cliui.Code("wush receive") + ". If not provided, it will be asked for on startup.",
				Default:     "",
				Value:       serpent.StringOf(&authID),
			},
			{
				Flag:    "stun-ip-override",
				Default: "",
				Value:   serpent.StringOf(&stunAddrOverride),
			},
			{
				Flag:        "wait-p2p",
				Description: "Waits for the connection to be p2p.",
				Default:     "false",
				Value:       serpent.BoolOf(&waitP2P),
			},
		},
	}
}
