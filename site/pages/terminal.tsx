import type { ReactElement } from "react";
import type { NextPageWithLayout } from "@/pages/_app";
import { useWasm } from "@/context/wush";
import { useEffect, useRef, useState, useContext, useCallback } from "react";
import type React from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { CanvasAddon } from "@xterm/addon-canvas";
// import { WebglAddon } from "@xterm/addon-webgl";
import "@xterm/xterm/css/xterm.css";
import { useRouter } from "next/router";
import { LogOut } from "lucide-react";

const TerminalPage: NextPageWithLayout = () => {
  const wasm = useWasm();
  const router = useRouter();
  const terminalRef = useRef<HTMLDivElement>(null);
  const terminalInstance = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon>();
  const sshSessionRef = useRef<WushSSHSession | null>();

  const handleDisconnect = useCallback(() => {
    if (sshSessionRef.current) {
      sshSessionRef.current.close();
    }
    router.push(`/access${window.location.hash}`);
  }, [router]);

  useEffect(() => {
    if (!wasm.wush.current) {
      console.log("WASM not initialized, skipping terminal initialization");
      return;
    }
    if (!wasm.connectedPeer) {
      console.log("No connected peer, skipping terminal initialization");
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

    const sshSession = wasm.wush.current.ssh(wasm.connectedPeer, {
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
      onConnectionProgress: (msg) => {},
      onConnected: () => {},
      onDone() {
        resizeObserver?.disconnect();
        term.dispose();
        console.log("term done");
        sshSession.close();
        sshSessionRef.current = null;
        router.push(`/access${window.location.hash}`);
      },
    });
    sshSessionRef.current = sshSession;
    term.onResize(({ rows, cols }) => sshSession.resize(rows, cols));

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
    };
  }, [wasm.wush.current, wasm.connectedPeer, router]);

  return (
    <div className="flex flex-col h-screen bg-[#282a36]">
      <div
        ref={terminalRef}
        className="flex-grow [&_.xterm-viewport]:!overflow-hidden [&_.xterm-screen]:!mb-0 [&_.xterm]:!p-0 flex"
      />
      <div className="h-6 bg-[#007ACC] text-white text-sm flex items-center justify-between px-2">
        <div className="flex items-center space-x-2">
          {/* Left side items can go here */}
        </div>
        <button
          type="button"
          onClick={handleDisconnect}
          className="hover:bg-[#1F8AD2] px-2 py-0.5 rounded flex items-center space-x-1"
        >
          <LogOut className="h-4 w-4" />
          <span>Disconnect</span>
        </button>
      </div>
    </div>
  );
};

TerminalPage.getLayout = function getLayout(page: ReactElement) {
  return page;
};

export default TerminalPage;
