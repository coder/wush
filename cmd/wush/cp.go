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
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/coder/wush/overlay"
	"github.com/coder/wush/tsserver"
	"github.com/schollz/progressbar/v3"
	"tailscale.com/net/netns"
	"tailscale.com/types/ptr"
)

func initLogger(verbose, quiet *bool, slogger *slog.Logger, logf *func(str string, args ...any)) serpent.MiddlewareFunc {
	return func(next serpent.HandlerFunc) serpent.HandlerFunc {
		return func(i *serpent.Invocation) error {
			if *verbose {
				*slogger = *slog.New(slog.NewTextHandler(i.Stderr, nil))
			} else {
				*slogger = *slog.New(slog.NewTextHandler(io.Discard, nil))
			}

			*logf = func(str string, args ...any) {
				if !*quiet {
					fmt.Fprintf(i.Stderr, str+"\n", args...)
				}
			}

			return next(i)
		}
	}
}

func initAuth(authFlag *string, ca *overlay.ClientAuth) serpent.MiddlewareFunc {
	return func(next serpent.HandlerFunc) serpent.HandlerFunc {
		return func(i *serpent.Invocation) error {
			if *authFlag == "" {
				err := huh.NewInput().
					Title("Enter your Auth ID:").
					Value(authFlag).
					Run()
				if err != nil {
					return fmt.Errorf("get auth id: %w", err)
				}
			}

			err := ca.Parse(strings.TrimSpace(*authFlag))
			if err != nil {
				return fmt.Errorf("parse auth key: %w", err)
			}

			return next(i)
		}
	}
}

func sendOverlayMW(opts *sendOverlayOpts, send **overlay.Send, logger *slog.Logger, logf *func(str string, args ...any)) serpent.MiddlewareFunc {
	return func(next serpent.HandlerFunc) serpent.HandlerFunc {
		return func(i *serpent.Invocation) error {
			dm, err := tsserver.DERPMapTailscale(i.Context())
			if err != nil {
				return err
			}

			newSend := overlay.NewSendOverlay(logger, dm)
			newSend.Auth = opts.clientAuth
			if opts.stunAddrOverride != "" {
				newSend.STUNIPOverride, err = netip.ParseAddr(opts.stunAddrOverride)
				if err != nil {
					return fmt.Errorf("parse stun addr override: %w", err)
				}
			}

			newSend.Auth.PrintDebug(*logf, dm)

			*send = newSend
			return next(i)
		}
	}
}

type sendOverlayOpts struct {
	authKey          string
	clientAuth       overlay.ClientAuth
	waitP2P          bool
	stunAddrOverride string
}

func cpCmd() *serpent.Command {
	var (
		verbose bool
		logger  = new(slog.Logger)
		logf    = func(str string, args ...any) {}

		overlayOpts = new(sendOverlayOpts)
		send        = new(overlay.Send)
	)
	return &serpent.Command{
		Use:   "cp <file>",
		Short: "Transfer files.",
		Long: "Transfer files to a " + cliui.Code("wush") + " peer.\n" + formatExamples(
			example{
				Description: "Copy a local file to the remote",
				Command:     "wush cp local-file.txt",
			},
		),
		Middleware: serpent.Chain(
			serpent.RequireNArgs(1),
			initLogger(&verbose, ptr.To(false), logger, &logf),
			initAuth(&overlayOpts.authKey, &overlayOpts.clientAuth),
			sendOverlayMW(overlayOpts, &send, logger, &logf),
		),
		Handler: func(inv *serpent.Invocation) error {
			ctx := inv.Context()

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
			ts, err := newTSNet("send", verbose)
			if err != nil {
				return err
			}
			defer ts.Close()

			logf("Bringing WireGuard up..")
			ts.Up(ctx)
			logf("WireGuard is ready!")

			lc, err := ts.LocalClient()
			if err != nil {
				return err
			}

			ip, err := waitUntilHasPeerHasIP(ctx, logf, lc)
			if err != nil {
				return err
			}

			if overlayOpts.waitP2P {
				err := waitUntilHasP2P(ctx, logf, lc)
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
				Flag:        "auth-key",
				Env:         "WUSH_AUTH_KEY",
				Description: "The auth key returned by " + cliui.Code("wush serve") + ". If not provided, it will be asked for on startup.",
				Default:     "",
				Value:       serpent.StringOf(&overlayOpts.authKey),
			},
			{
				Flag:    "stun-ip-override",
				Default: "",
				Value:   serpent.StringOf(&overlayOpts.stunAddrOverride),
			},
			{
				Flag:        "wait-p2p",
				Description: "Waits for the connection to be p2p.",
				Default:     "false",
				Value:       serpent.BoolOf(&overlayOpts.waitP2P),
			},
			{
				Flag:          "verbose",
				FlagShorthand: "v",
				Description:   "Enable verbose logging.",
				Default:       "false",
				Value:         serpent.BoolOf(&verbose),
			},
		},
	}
}
