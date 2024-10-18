//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"github.com/coder/wush/overlay"
	"github.com/coder/wush/tsserver"
	"golang.org/x/crypto/ssh"
	"golang.org/x/xerrors"
	"tailscale.com/ipn/store"
	"tailscale.com/net/netns"
	"tailscale.com/tsnet"
)

func main() {
	fmt.Println("WebAssembly module initialized")
	defer fmt.Println("WebAssembly module exited")

	js.Global().Set("newWush", js.FuncOf(func(this js.Value, args []js.Value) any {
		handler := js.FuncOf(func(this js.Value, promiseArgs []js.Value) any {
			if len(args) != 1 {
				log.Fatal("Usage: newWush(config)")
				return nil
			}

			go func() {
				w := newWush(args[0])
				promiseArgs[0].Invoke(w)
			}()

			return nil
		})

		promiseConstructor := js.Global().Get("Promise")
		return promiseConstructor.New(handler)
	}))
	js.Global().Set("exitWush", js.FuncOf(func(this js.Value, args []js.Value) any {
		// close(ch)
		return nil
	}))

	// Keep the main function running
	<-make(chan struct{}, 0)
}

func newWush(cfg js.Value) map[string]any {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(jsConsoleWriter{}, nil))
	hlog := func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	}
	dm, err := tsserver.DERPMapTailscale(ctx)
	if err != nil {
		panic(err)
	}

	ov := overlay.NewWasmOverlay(log.Printf, dm, cfg.Get("onNewPeer"))

	err = ov.PickDERPHome(ctx)
	if err != nil {
		panic(err)
	}

	s, err := tsserver.NewServer(ctx, logger, ov, dm)
	if err != nil {
		panic(err)
	}

	go ov.ListenOverlayDERP(ctx)
	go s.ListenAndServe(ctx)
	netns.SetDialerOverride(s.Dialer())

	ts, err := newTSNet("send")
	if err != nil {
		panic(err)
	}

	_, err = ts.Up(ctx)
	if err != nil {
		panic(err)
	}
	hlog("WireGuard is ready")

	cpListener, err := ts.Listen("tcp", ":4444")
	if err != nil {
		panic(err)
	}

	go func() {
		err := http.Serve(cpListener, http.HandlerFunc(cpH(
			cfg.Get("onIncomingFile"),
			cfg.Get("downloadFile"),
		)))
		if err != nil {
			hlog("File transfer server exited: " + err.Error())
		}
	}()

	return map[string]any{
		"auth_info": js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) != 0 {
				log.Printf("Usage: auth_info()")
				return nil
			}

			return map[string]any{
				"derp_id":   ov.DerpRegionID,
				"derp_name": ov.DerpMap.Regions[int(ov.DerpRegionID)].RegionName,
				"auth_key":  ov.ClientAuth().AuthKey(),
			}
		}),
		"stop": js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) != 0 {
				log.Printf("Usage: stop()")
				return nil
			}
			cpListener.Close()
			ts.Close()
			return nil
		}),
		"ssh": js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) != 1 {
				log.Printf("Usage: ssh({})")
				return nil
			}

			sess := &sshSession{
				ts:  ts,
				cfg: args[0],
			}

			go sess.Run()

			return map[string]any{
				"close": js.FuncOf(func(this js.Value, args []js.Value) any {
					return sess.Close() != nil
				}),
				"resize": js.FuncOf(func(this js.Value, args []js.Value) any {
					rows := args[0].Int()
					cols := args[1].Int()
					return sess.Resize(rows, cols) != nil
				}),
			}
		}),
		"connect": js.FuncOf(func(this js.Value, args []js.Value) any {
			handler := js.FuncOf(func(this js.Value, promiseArgs []js.Value) any {
				resolve := promiseArgs[0]
				reject := promiseArgs[1]

				go func() {
					if len(args) != 1 {
						errorConstructor := js.Global().Get("Error")
						errorObject := errorConstructor.New("Usage: connect(authKey)")
						reject.Invoke(errorObject)
						return
					}

					var authKey string
					if args[0].Type() == js.TypeString {
						authKey = args[0].String()
					} else {
						errorConstructor := js.Global().Get("Error")
						errorObject := errorConstructor.New("Usage: connect(authKey)")
						reject.Invoke(errorObject)
						return
					}

					var ca overlay.ClientAuth
					err := ca.Parse(authKey)
					if err != nil {
						errorConstructor := js.Global().Get("Error")
						errorObject := errorConstructor.New(fmt.Errorf("parse authkey: %w", err).Error())
						reject.Invoke(errorObject)
						return
					}

					ctx, cancel := context.WithCancel(context.Background())
					peer, err := ov.Connect(ctx, ca)
					if err != nil {
						cancel()
						errorConstructor := js.Global().Get("Error")
						errorObject := errorConstructor.New(fmt.Errorf("parse authkey: %w", err).Error())
						reject.Invoke(errorObject)
						return
					}

					resolve.Invoke(map[string]any{
						"id":   js.ValueOf(peer.ID),
						"name": js.ValueOf(peer.Name),
						"ip":   js.ValueOf(peer.IP.String()),
						"cancel": js.FuncOf(func(this js.Value, args []js.Value) any {
							cancel()
							return nil
						}),
					})
				}()

				return nil
			})

			promiseConstructor := js.Global().Get("Promise")
			return promiseConstructor.New(handler)
		}),
		"transfer": js.FuncOf(func(this js.Value, args []js.Value) any {
			handler := js.FuncOf(func(this js.Value, promiseArgs []js.Value) any {
				resolve := promiseArgs[0]
				reject := promiseArgs[1]

				if len(args) != 5 {
					errorConstructor := js.Global().Get("Error")
					errorObject := errorConstructor.New("Usage: transfer(peer, file)")
					reject.Invoke(errorObject)
					return nil
				}

				peer := args[0]
				ip := peer.Get("ip").String()
				fileName := args[1].String()
				sizeBytes := args[2].Int()
				stream := args[3]
				streamHelper := args[4]

				pr, pw := io.Pipe()

				goCallback := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
					promiseConstructor := js.Global().Get("Promise")
					return promiseConstructor.New(js.FuncOf(func(this js.Value, promiseArgs []js.Value) any {
						resolve := promiseArgs[0]
						_ = promiseArgs[1]
						go func() {
							if len(args) == 0 || args[0].IsNull() || args[0].IsUndefined() {
								pw.Close()
								resolve.Invoke()
								return
							}

							fmt.Println("in go callback")
							// Convert the JavaScript Uint8Array to a Go byte slice
							uint8Array := args[0]
							fmt.Println("type is", uint8Array.Type().String())
							length := uint8Array.Get("length").Int()
							buf := make([]byte, length)
							js.CopyBytesToGo(buf, uint8Array)

							fmt.Println("sending data to channel")
							// Send the data to the channel
							if _, err := pw.Write(buf); err != nil {
								pw.CloseWithError(err)
							}
							fmt.Println("callback finished")

							// Resolve the promise
							resolve.Invoke()
						}()
						return nil
					}))
				})

				go func() {
					defer goCallback.Release()

					streamHelper.Invoke(stream, goCallback)

					hc := ts.HTTPClient()
					req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s:4444/%s", ip, fileName), pr)
					if err != nil {
						errorConstructor := js.Global().Get("Error")
						errorObject := errorConstructor.New(err.Error())
						reject.Invoke(errorObject)
						return
					}
					req.ContentLength = int64(sizeBytes)

					res, err := hc.Do(req)
					if err != nil {
						errorConstructor := js.Global().Get("Error")
						errorObject := errorConstructor.New(err.Error())
						reject.Invoke(errorObject)
						return
					}
					defer res.Body.Close()

					bod := bytes.NewBuffer(nil)
					_, _ = io.Copy(bod, res.Body)

					fmt.Println(bod.String())
					resolve.Invoke()
				}()

				return nil
			})

			promiseConstructor := js.Global().Get("Promise")
			return promiseConstructor.New(handler)
		}),
	}
}

type sshSession struct {
	ts  *tsnet.Server
	cfg js.Value

	session           *ssh.Session
	pendingResizeRows int
	pendingResizeCols int
}

func (s *sshSession) Close() error {
	if s.session == nil {
		// We never had a chance to open the session, ignore the close request.
		return nil
	}
	return s.session.Close()
}

func (s *sshSession) Resize(rows, cols int) error {
	if s.session == nil {
		s.pendingResizeRows = rows
		s.pendingResizeCols = cols
		return nil
	}
	return s.session.WindowChange(rows, cols)
}

func (s *sshSession) Run() {
	writeFn := s.cfg.Get("writeFn")
	writeErrorFn := s.cfg.Get("writeErrorFn")
	setReadFn := s.cfg.Get("setReadFn")
	rows := s.cfg.Get("rows").Int()
	cols := s.cfg.Get("cols").Int()
	timeoutSeconds := 5.0
	if jsTimeoutSeconds := s.cfg.Get("timeoutSeconds"); jsTimeoutSeconds.Type() == js.TypeNumber {
		timeoutSeconds = jsTimeoutSeconds.Float()
	}
	onConnectionProgress := s.cfg.Get("onConnectionProgress")
	onConnected := s.cfg.Get("onConnected")
	onDone := s.cfg.Get("onDone")
	defer onDone.Invoke()

	writeError := func(label string, err error) {
		writeErrorFn.Invoke(fmt.Sprintf("%s Error: %v\r\n", label, err))
	}
	reportProgress := func(message string) {
		onConnectionProgress.Invoke(message)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds*float64(time.Second)))
	defer cancel()
	reportProgress(fmt.Sprintf("Connecting..."))
	c, err := s.ts.Dial(ctx, "tcp", net.JoinHostPort("100.64.0.0", "3"))
	if err != nil {
		writeError("Dial", err)
		return
	}
	defer c.Close()

	config := &ssh.ClientConfig{
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			// Host keys are not used with Tailscale SSH, but we can use this
			// callback to know that the connection has been established.
			reportProgress("SSH connection established…")
			return nil
		},
	}

	reportProgress("Starting SSH client…")
	sshConn, _, _, err := ssh.NewClientConn(c, "100.64.0.0:3", config)
	if err != nil {
		writeError("SSH Connection", err)
		return
	}
	defer sshConn.Close()

	sshClient := ssh.NewClient(sshConn, nil, nil)
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		writeError("SSH Session", err)
		return
	}
	s.session = session
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		writeError("SSH Stdin", err)
		return
	}

	session.Stdout = termWriter{writeFn}
	session.Stderr = termWriter{writeFn}

	setReadFn.Invoke(js.FuncOf(func(this js.Value, args []js.Value) any {
		input := args[0].String()
		_, err := stdin.Write([]byte(input))
		if err != nil {
			writeError("Write Input", err)
		}
		return nil
	}))

	// We might have gotten a resize notification since we started opening the
	// session, pick up the latest size.
	if s.pendingResizeRows != 0 {
		rows = s.pendingResizeRows
	}
	if s.pendingResizeCols != 0 {
		cols = s.pendingResizeCols
	}
	err = session.RequestPty("xterm", rows, cols, ssh.TerminalModes{})

	if err != nil {
		writeError("Pseudo Terminal", err)
		return
	}

	err = session.Shell()
	if err != nil {
		writeError("Shell", err)
		return
	}

	onConnected.Invoke()
	err = session.Wait()
	if err != nil {
		writeError("Wait", err)
		return
	}
}

type termWriter struct {
	f js.Value
}

func (w termWriter) Write(p []byte) (n int, err error) {
	r := bytes.Replace(p, []byte("\n"), []byte("\n\r"), -1)
	w.f.Invoke(string(r))
	return len(p), nil
}

type jsConsoleWriter struct{}

func (w jsConsoleWriter) Write(p []byte) (n int, err error) {
	js.Global().Get("console").Call("log", string(p))
	return len(p), nil
}

func newTSNet(direction string) (*tsnet.Server, error) {
	var err error
	// tmp := os.TempDir()
	srv := new(tsnet.Server)
	// srv.Dir = tmp
	srv.Hostname = "wush-" + direction
	srv.Ephemeral = true
	srv.AuthKey = direction
	srv.ControlURL = "http://127.0.0.1:8080"
	// srv.Logf = func(format string, args ...any) {}
	srv.Logf = func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	}
	// srv.UserLogf = func(format string, args ...any) {}
	srv.UserLogf = func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	}
	// netns.SetEnabled(false)

	srv.Store, err = store.New(func(format string, args ...any) {}, "mem:wush")
	if err != nil {
		return nil, xerrors.Errorf("create state store: %w", err)
	}

	return srv, nil
}

func cpH(onIncomingFile js.Value, downloadFile js.Value) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return
		}

		fiName := strings.TrimPrefix(r.URL.Path, "/")

		// TODO: impl
		peer := map[string]any{
			"id":   js.ValueOf(0),
			"name": js.ValueOf(""),
			"ip":   js.ValueOf(""),
			"cancel": js.FuncOf(func(this js.Value, args []js.Value) any {
				return nil
			}),
		}

		var allow bool
		onIncomingFile.Invoke(peer, fiName, r.ContentLength).
			Call("then", js.FuncOf(func(this js.Value, args []js.Value) any {
				allow = args[0].Bool()
				return nil
			})).
			Call("catch", js.FuncOf(func(this js.Value, args []js.Value) any {
				fmt.Println("onIncomingFile failed:", args[0].String())
				allow = false
				return nil
			}))
		if !allow {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("File transfer was denied"))
			r.Body.Close()
			return
		}

		underlyingSource := map[string]interface{}{
			// start method
			"start": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				// The first and only arg is the controller object
				controller := args[0]

				// Process the stream in yet another background goroutine,
				// because we can't block on a goroutine invoked by JS in Wasm
				// that is dealing with HTTP requests
				go func() {
					// Close the response body at the end of this method
					defer r.Body.Close()

					// Read the entire stream and pass it to JavaScript
					for {
						// Read up to 16KB at a time
						buf := make([]byte, 16384)
						n, err := r.Body.Read(buf)
						if err != nil && err != io.EOF {
							// Tell the controller we have an error
							// We're ignoring "EOF" however, which means the stream was done
							errorConstructor := js.Global().Get("Error")
							errorObject := errorConstructor.New(err.Error())
							controller.Call("error", errorObject)
							return
						}
						if n > 0 {
							// If we read anything, send it to JavaScript using the "enqueue" method on the controller
							// We need to convert it to a Uint8Array first
							arrayConstructor := js.Global().Get("Uint8Array")
							dataJS := arrayConstructor.New(n)
							js.CopyBytesToJS(dataJS, buf[0:n])
							controller.Call("enqueue", dataJS)
						}
						if err == io.EOF {
							// Stream is done, so call the "close" method on the controller
							controller.Call("close")
							return
						}
					}
				}()

				return nil
			}),
			// cancel method
			"cancel": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				// If the request is canceled, just close the body
				r.Body.Close()

				return nil
			}),
		}

		readableStreamConstructor := js.Global().Get("ReadableStream")
		readableStream := readableStreamConstructor.New(underlyingSource)

		downloadFile.Invoke(peer, fiName, r.ContentLength, readableStream)
	}
}
