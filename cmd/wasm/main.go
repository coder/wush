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

func newWush(jsConfig js.Value) map[string]any {
	ctx := context.Background()
	var authKey string
	if jsAuthKey := jsConfig.Get("authKey"); jsAuthKey.Type() == js.TypeString {
		authKey = jsAuthKey.String()
	}

	logger := slog.New(slog.NewTextHandler(jsConsoleWriter{}, nil))
	hlog := func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	}
	dm, err := tsserver.DERPMapTailscale(ctx)
	if err != nil {
		panic(err)
	}

	send := overlay.NewSendOverlay(logger, dm)
	err = send.Auth.Parse(authKey)
	if err != nil {
		panic(err)
	}

	s, err := tsserver.NewServer(ctx, logger, send)
	if err != nil {
		panic(err)
	}

	go send.ListenOverlayDERP(ctx)
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

	return map[string]any{
		"stop": js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) != 0 {
				log.Printf("Usage: stop()")
				return nil
			}
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
		"share": js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) != 1 {
				log.Printf("Usage: ssh({})")
				return nil
			}

			sess := &shareSession{
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

type shareSession struct {
	ts  *tsnet.Server
	cfg js.Value

	conn              net.Conn
	pendingResizeRows int
	pendingResizeCols int
}

func (s *shareSession) Close() error {
	if s.conn == nil {
		// We never had a chance to open the session, ignore the close request.
		return nil
	}
	return s.conn.Close()
}

func (s *shareSession) Resize(rows, cols int) error {
	if s.conn == nil {
		s.pendingResizeRows = rows
		s.pendingResizeCols = cols
		return nil
	}

	return nil
	// return s.session.WindowChange(rows, cols)
}

func (s *shareSession) Run() {
	writeFn := s.cfg.Get("writeFn")
	writeErrorFn := s.cfg.Get("writeErrorFn")
	setReadFn := s.cfg.Get("setReadFn")
	// rows := s.cfg.Get("rows").Int()
	// cols := s.cfg.Get("cols").Int()
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
	c, err := s.ts.Dial(ctx, "tcp", net.JoinHostPort("100.64.0.0", "33"))
	if err != nil {
		writeError("Dial", err)
		return
	}
	defer c.Close()
	s.conn = c
	reportProgress(fmt.Sprintf("Connected"))

	setReadFn.Invoke(js.FuncOf(func(this js.Value, args []js.Value) any {
		input := args[0].String()
		_, err := c.Write([]byte(input))
		if err != nil {
			writeError("Write Input", err)
		}
		return nil
	}))

	onConnected.Invoke()
	tw := termWriter{writeFn}
	_, _ = io.Copy(tw, c)
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
