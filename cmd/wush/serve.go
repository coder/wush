package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/afero"
	xslices "golang.org/x/exp/slices"
	"golang.org/x/xerrors"
	"tailscale.com/ipn/store"
	"tailscale.com/net/netns"
	"tailscale.com/tsnet"

	cslog "cdr.dev/slog"
	csloghuman "cdr.dev/slog/sloggers/sloghuman"
	"github.com/coder/coder/v2/agent/agentssh"
	"github.com/coder/pretty"
	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/coder/wush/overlay"
	"github.com/coder/wush/tsserver"
)

func serveCmd() *serpent.Command {
	var (
		overlayType string
		verbose     bool
		enabled     = []string{}
		disabled    = []string{}
		quiet       bool
	)

	// New function to handle conditional printing
	print := func(a ...any) {
		if !quiet {
			fmt.Println(a...)
		}
	}

	return &serpent.Command{
		Use:     "serve",
		Aliases: []string{"host"},
		Short:   "Run the wush server.",
		Long:    "Runs the wush server. Allows other wush CLIs to connect to this computer.",
		Handler: func(inv *serpent.Invocation) error {
			ctx := inv.Context()
			var logSink io.Writer = io.Discard
			if verbose {
				logSink = inv.Stderr
			}
			logger := slog.New(slog.NewTextHandler(logSink, nil))
			dm, err := tsserver.DERPMapTailscale(ctx)
			if err != nil {
				return err
			}
			r := overlay.NewReceiveOverlay(logger, dm)

			switch overlayType {
			case "derp":
				err = r.PickDERPHome(ctx)
				if err != nil {
					return err
				}
				go r.ListenOverlayDERP(ctx)

			case "stun":
				waitStun, err := r.ListenOverlaySTUN(ctx)
				if err != nil {
					return fmt.Errorf("get stun addr: %w", err)
				}
				<-waitStun

			default:
				return fmt.Errorf("unknown overlay type: %s", overlayType)
			}

			if quiet {
				// Print only the auth key
				fmt.Println(r.ClientAuth().AuthKey())
			} else {
				// Print the regular output only if not in quiet mode
				print("Your auth key is:")
				print("\t>", cliui.Code(r.ClientAuth().AuthKey()))
				print("Use this key to authenticate other", cliui.Code("wush"), "commands to this instance.")
			}

			s, err := tsserver.NewServer(ctx, logger, r)
			if err != nil {
				return err
			}

			go s.ListenAndServe(ctx)
			netns.SetDialerOverride(s.Dialer())
			ts, err := newTSNet("receive")
			if err != nil {
				return err
			}

			ts.Up(ctx)
			fs := afero.NewOsFs()

			print(cliui.Timestamp(time.Now()), "WireGuard is ready")

			closers := []io.Closer{}

			if xslices.Contains(enabled, "ssh") && !xslices.Contains(disabled, "ssh") {
				sshSrv, err := agentssh.NewServer(ctx,
					cslog.Make(csloghuman.Sink(logSink)),
					prometheus.NewRegistry(),
					fs,
					nil,
				)
				if err != nil {
					return err
				}
				closers = append(closers, sshSrv)

				sshListener, err := ts.Listen("tcp", ":3")
				if err != nil {
					return err
				}
				closers = append(closers, sshListener)

				print(cliui.Timestamp(time.Now()), "SSH server "+pretty.Sprint(cliui.DefaultStyles.Enabled, "enabled"))
				go func() {
					err := sshSrv.Serve(sshListener)
					if err != nil {
						print(cliui.Timestamp(time.Now()), "SSH server exited: "+err.Error())
					}
				}()
			} else {
				print(cliui.Timestamp(time.Now()), "SSH server "+pretty.Sprint(cliui.DefaultStyles.Disabled, "disabled"))
			}

			if xslices.Contains(enabled, "cp") && !xslices.Contains(disabled, "cp") {
				cpListener, err := ts.Listen("tcp", ":4444")
				if err != nil {
					return err
				}
				closers = append([]io.Closer{cpListener}, closers...)

				print(cliui.Timestamp(time.Now()), "File transfer server "+pretty.Sprint(cliui.DefaultStyles.Enabled, "enabled"))
				go func() {
					err := http.Serve(cpListener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						cpHandler(w, r, quiet)
					}))
					if err != nil {
						print(cliui.Timestamp(time.Now()), "File transfer server exited: "+err.Error())
					}
				}()
			} else {
				print(cliui.Timestamp(time.Now()), "File transfer server "+pretty.Sprint(cliui.DefaultStyles.Disabled, "disabled"))
			}

			if xslices.Contains(enabled, "port-forward") && !xslices.Contains(disabled, "port-forward") {
				ts.RegisterFallbackTCPHandler(func(src, dst netip.AddrPort) (handler func(net.Conn), intercept bool) {
					return func(src net.Conn) {
						dst, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", dst.Port()))
						if err != nil {
							print("failed to dial forwarded connection:", err.Error())
							src.Close()
							return
						}

						bicopy(ctx, src, dst)
					}, true
				})
			} else {
				print(cliui.Timestamp(time.Now()), "Port-forward server "+pretty.Sprint(cliui.DefaultStyles.Disabled, "disabled"))
			}

			ctx, ctxCancel := inv.SignalNotifyContext(ctx, os.Interrupt)
			defer ctxCancel()

			closers = append(closers, ts)
			<-ctx.Done()
			for _, closer := range closers {
				closer.Close()
			}
			return nil
		},
		Options: []serpent.Option{
			{
				Flag:    "overlay-type",
				Default: "derp",
				Value:   serpent.EnumOf(&overlayType, "derp", "stun"),
			},
			{
				Flag:          "verbose",
				FlagShorthand: "v",
				Description:   "Enable verbose logging.",
				Default:       "false",
				Value:         serpent.BoolOf(&verbose),
			},
			{
				Flag:        "enable",
				Description: "Server options to enable.",
				Default:     "ssh,cp,port-forward",
				Value:       serpent.EnumArrayOf(&enabled, "ssh", "cp", "port-forward"),
			},
			{
				Flag:        "disable",
				Description: "Server options to disable.",
				Default:     "",
				Value:       serpent.EnumArrayOf(&disabled, "ssh", "cp", "port-forward"),
			},
			{
				Flag:          "quiet",
				FlagShorthand: "q",
				Description:   "Disables all logging except for the auth key.",
				Default:       "false",
				Value:         serpent.BoolOf(&quiet),
			},
		},
	}
}

func newTSNet(direction string) (*tsnet.Server, error) {
	var err error
	tmp := os.TempDir()
	srv := new(tsnet.Server)
	srv.Dir = tmp
	srv.Hostname = "wush-" + direction
	srv.Ephemeral = true
	srv.AuthKey = direction
	srv.ControlURL = "http://localhost:8080"
	srv.Logf = func(format string, args ...any) {}
	// srv.Logf = func(format string, args ...any) {
	// 	fmt.Printf(format+"\n", args...)
	// }
	srv.UserLogf = func(format string, args ...any) {}
	// srv.UserLogf = func(format string, args ...any) {
	// 	fmt.Printf(format+"\n", args...)
	// }

	srv.Store, err = store.New(func(format string, args ...any) {}, "mem:wush")
	if err != nil {
		return nil, xerrors.Errorf("create state store: %w", err)
	}

	return srv, nil
}

func bicopy(ctx context.Context, c1, c2 io.ReadWriteCloser) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer func() {
		_ = c1.Close()
		_ = c2.Close()
	}()

	var wg sync.WaitGroup
	copyFunc := func(dst io.WriteCloser, src io.Reader) {
		defer func() {
			wg.Done()
			// If one side of the copy fails, ensure the other one exits as
			// well.
			cancel()
		}()
		_, _ = io.Copy(dst, src)
	}

	wg.Add(2)
	go copyFunc(c1, c2)
	go copyFunc(c2, c1)

	// Convert waitgroup to a channel so we can also wait on the context.
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()

	select {
	case <-ctx.Done():
	case <-done:
	}
}

func cpHandler(w http.ResponseWriter, r *http.Request, quiet bool) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return
	}

	fiName := strings.TrimPrefix(r.URL.Path, "/")
	defer r.Body.Close()

	fi, err := os.OpenFile(fiName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var writer io.Writer = fi
	var bar *progressbar.ProgressBar

	if !quiet {
		bar = progressbar.DefaultBytes(
			r.ContentLength,
			fmt.Sprintf("Downloading %q", fiName),
		)
		writer = io.MultiWriter(fi, bar)
	}

	_, err = io.Copy(writer, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fi.Close()
	if bar != nil {
		bar.Close()
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("File %q written", fiName)))

	if !quiet {
		fmt.Println("Received file", fiName, "from", r.RemoteAddr)
	}
}
