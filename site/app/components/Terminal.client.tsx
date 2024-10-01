import { useEffect, useRef, useState, useContext } from "react";
import type React from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { CanvasAddon } from "@xterm/addon-canvas";
// import { WebglAddon } from "@xterm/addon-webgl";
import { WushContext } from "~/context/wush";
import "@xterm/xterm/css/xterm.css";

interface WushTerminalProps {
  authKey: string;
}

const WushTerminal: React.FC<WushTerminalProps> = ({ authKey }) => {
  const terminalRef = useRef<HTMLDivElement>(null);
  const terminalInstance = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon>();
  const wushInitialized = useContext(WushContext);
  const sshSessionRef = useRef<WushSSHSession | null>();
  const wushRef = useRef<Wush | null>();

  useEffect(() => {
    if (!wushInitialized) {
      console.log("WASM not initialized, skipping terminal initialization");
      return;
    }

    console.log("Terminal component mounted");

    if (!terminalRef.current) {
      console.log("Terminal ref is null, skipping terminal initialization");
      return;
    }

    if (terminalInstance.current) {
      console.log("Terminal already initialized, skipping");
      return;
    }

    console.log("running wush");

    console.log("Initializing terminal");

    const term = new Terminal({
      cursorBlink: true,
      theme: {
        background: "#282a36",
        foreground: "#f8f8f2",
      },
      scrollback: 0,
    });
    const fitAddon = new FitAddon();
    fitAddonRef.current = fitAddon;
    term.loadAddon(fitAddon);
    term.loadAddon(new CanvasAddon());
    // term.loadAddon(new WebglAddon());
    term.open(terminalRef.current);
    fitAddon.fit();

    let onDataHook: ((data: string) => void) | undefined;
    term.onData((e) => {
      onDataHook?.(e);
    });

    const resizeObserver = new window.ResizeObserver(() => fitAddon.fit());
    resizeObserver.observe(terminalRef.current);

    newWush({ authKey: authKey }).then((wush) => {
      wushRef.current = wush;
      const sshSession = wush.share({
        writeFn(input) {
          term.write(input);
        },
        writeErrorFn(err) {
          term.write(err);
        },
        setReadFn(hook) {
          onDataHook = hook;
        },
        rows: term.rows,
        cols: term.cols,
        onConnectionProgress: (msg) => {
          term.writeln(msg);
        },
        onConnected: () => {
          term.writeln("");
          term.clear();
        },
        onDone() {
          resizeObserver?.disconnect();
          term.dispose();
          console.log("term done");
          sshSession.close();
          sshSessionRef.current = null;
        },
      });

      sshSessionRef.current = sshSession;
      term.onResize(({ rows, cols }) => sshSession.resize(rows, cols));
    });

    console.log("Terminal initialized and opened");
    terminalInstance.current = term;
    fitAddon.fit();

    return () => {
      console.log("Disposing terminal");
      if (terminalInstance.current) {
        resizeObserver.disconnect();
        terminalInstance.current.dispose();
        terminalInstance.current = null;
      }
      if (sshSessionRef.current) {
        sshSessionRef.current.close();
        sshSessionRef.current = null;
      }
      if (wushRef.current) {
        wushRef.current.stop();
        wushRef.current = null;
      }
    };
  }, [authKey, wushInitialized]);

  return <div ref={terminalRef} className="h-full" />;
};

export default WushTerminal;
