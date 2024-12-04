import { useEffect, useRef, useCallback } from "react";
import type { ReactElement } from "react";
import type { NextPageWithLayout } from "@/pages/_app";
import { useWasm } from "@/context/wush";
import { useRouter } from "next/router";
import { LogOut } from "lucide-react";
import "@xterm/xterm/css/xterm.css";

const TerminalPage: NextPageWithLayout = () => {
  const wasm = useWasm();
  const router = useRouter();
  const terminalRef = useRef<HTMLDivElement>(null);
  const terminalInstance = useRef<any>(null);
  const fitAddonRef = useRef<any>(null);
  const sshSessionRef = useRef<WushSSHSession | null>(null);
  const isInitializing = useRef(false);

  const handleDisconnect = useCallback(() => {
    if (sshSessionRef.current) {
      sshSessionRef.current.close();
    }
    router.push(`/access${window.location.hash}`);
  }, [router]);

  useEffect(() => {
    if (isInitializing.current) return;
    isInitializing.current = true;

    const initializeTerminal = async () => {
      // Dynamically import xterm.js and its addons
      const { Terminal } = await import("@xterm/xterm");
      const { FitAddon } = await import("@xterm/addon-fit");
      const { CanvasAddon } = await import("@xterm/addon-canvas");

      if (!terminalRef.current) {
        console.log("Terminal ref is null, skipping terminal initialization");
        return;
      }

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
      term.open(terminalRef.current);
      fitAddon.fit();

      let onDataHook: ((data: string) => void) | undefined;
      term.onData((e) => {
        onDataHook?.(e);
      });

      const resizeObserver = new ResizeObserver(() => fitAddon.fit());
      resizeObserver.observe(terminalRef.current);

      if (wasm.wush.current && wasm.connectedPeer) {
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
            resizeObserver.disconnect();
            term.dispose();
            sshSession.close();
            sshSessionRef.current = null;
            router.push(`/access${window.location.hash}`);
          },
        });
        sshSessionRef.current = sshSession;
        term.onResize(({ rows, cols }) => sshSession.resize(rows, cols));
      }

      term.focus();
      terminalInstance.current = term;
      isInitializing.current = false;
    };

    initializeTerminal();

    return () => {
      console.log("Disposing terminal");
      if (terminalInstance.current) {
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
        className="flex-grow min-h-0 flex [&_.xterm-viewport]:!overflow-hidden [&_.xterm-screen]:!mb-0 [&_.xterm]:!p-0"
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
