import type { ReactNode } from "react";
import { useState } from "react";
import { Star, Plug, PlugZap, Download } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/router";
import { useWasm } from "@/context/wush";
import { Toaster } from "@/components/ui/sonner";
import Head from "next/head";

const LoadingSpinner = ({ className }: { className?: string }) => (
  <svg
    className={className}
    xmlns="http://www.w3.org/2000/svg"
    fill="none"
    viewBox="0 0 24 24"
    role="img"
    aria-label="Loading spinner"
  >
    <title>Loading spinner</title>
    <circle
      className="opacity-25"
      cx="12"
      cy="12"
      r="10"
      stroke="currentColor"
      strokeWidth="4"
    />
    <path
      className="opacity-75"
      fill="currentColor"
      d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
    />
  </svg>
);

export default function Layout({ children }: { children: ReactNode }) {
  const router = useRouter();
  const activeTab = router.pathname.substring(1);
  const wasm = useWasm();
  const currentFragment = (() => {
    // Check if we're in the browser
    return typeof window !== "undefined"
      ? window.location.hash.substring(1)
      : "";
  })();

  const [pendingPeer, setPendingPeer] = useState<string>(currentFragment);

  return (
    <>
      <Head>
        <title>wush</title>
        <meta name="description" content="Secure peer-to-peer file transfer" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <link rel="icon" href="/favicon.ico" />
      </Head>
      <div className="min-h-screen flex flex-col bg-black text-gray-200">
        <header className="p-6 flex justify-between items-center bg-gray-950 border-b border-gray-800">
          <div className="flex items-center space-x-4">
            <h3 className="text-2xl font-bold text-white">⧉ wush</h3>
            <span className="text-sm text-gray-400 hidden sm:inline">
              v0.4.0
            </span>
            <div className="flex items-center space-x-2">
              <div
                className={`w-2 h-2 rounded-full shadow-[0_0_8px] ${
                  wasm.wush?.current
                    ? "bg-green-500 shadow-green-500/50"
                    : "bg-red-500 shadow-red-500/50 animate-pulse"
                }`}
              />
              <span
                className="text-sm text-gray-400"
                title="The currently connected DERP region. DERP servers help establish connections between peers when direct connections aren't possible"
              >
                {wasm.wush.current?.auth_info()?.derp_name || "Connecting..."}
              </span>
            </div>
          </div>
          <div className="flex items-center space-x-2">
            <a
              href="https://github.com/coder/wush/releases/latest"
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center text-gray-400 hover:text-gray-200 transition-colors"
            >
              <Download className="h-5 w-5 mr-1" />
              <span>Download the CLI</span>
            </a>
            <span className="text-gray-400">•</span>
            <a
              href="https://github.com/coder/wush"
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center text-gray-400 hover:text-gray-200 transition-colors"
            >
              <Star className="h-5 w-5 mr-1" />
              <span>{process.env.GITHUB_STARS}</span>
            </a>
          </div>
        </header>

        <main className="flex-1 p-8 pt-40 bg-gradient-to-br from-gray-950 via-gray-900 to-black">
          <div className="w-full max-w-lg mx-auto">
            <div className="text-center space-y-4">
              <h1 className="text-4xl font-bold text-white">
                Send, Receive, Access
              </h1>
              <p className="text-gray-400">
                WireGuard-powered peer-to-peer file transfer and remote access
              </p>
              <div className="flex justify-center items-center space-x-2 text-sm text-gray-500">
                <span>Unlimited File Size</span>
                <span>•</span>
                <span>E2E Encrypted</span>
                <span>•</span>
                <span>Command Line ↔ Browser</span>
              </div>
            </div>

            <div className="bg-gray-800 rounded-lg shadow-xl overflow-hidden border border-gray-700 mt-8 p-6 space-y-2">
              <span>Current peer</span>
              <div className="flex">
                <input
                  type="text"
                  value={pendingPeer}
                  className="flex-grow p-3 border border-gray-600 rounded bg-gray-700 text-gray-200"
                  onChange={(e) => setPendingPeer(e.target.value)}
                  readOnly={Boolean(wasm.connectedPeer)}
                  placeholder="Enter auth key"
                />
              </div>
              <button
                type="submit"
                onClick={async () => {
                  if (wasm.connectedPeer) {
                    router.push("#");
                    setPendingPeer("");
                  } else {
                    router.push(`#${pendingPeer}`);
                  }
                }}
                disabled={wasm.loading || wasm.connecting}
                className={`p-3 w-full rounded flex items-center justify-center transition-colors ${
                  wasm.loading || wasm.connecting
                    ? "bg-gray-600 cursor-not-allowed"
                    : wasm.connectedPeer
                    ? "bg-green-600 hover:bg-green-700"
                    : "bg-blue-600 hover:bg-blue-700"
                }`}
              >
                {wasm.loading ? (
                  <>
                    <LoadingSpinner className="mr-2 h-5 w-5 animate-spin" />
                    Initializing...
                  </>
                ) : wasm.connecting ? (
                  <>
                    <LoadingSpinner className="mr-2 h-5 w-5 animate-spin" />
                    Connecting...
                  </>
                ) : wasm.connectedPeer ? (
                  <>
                    <PlugZap className="mr-2 h-5 w-5" />
                    Disconnect
                  </>
                ) : (
                  <>
                    <Plug className="mr-2 h-5 w-5" />
                    Connect
                  </>
                )}
              </button>
              <span>{wasm.error}</span>
            </div>

            <div className="bg-gray-800 rounded-lg shadow-xl overflow-hidden border border-gray-700 mt-4">
              <div className="flex border-b border-gray-700 relative text-center">
                {["send", "receive", "access"].map((tab) => (
                  <Link
                    key={tab}
                    href={`/${tab}#${currentFragment}`}
                    className="flex-1 py-3 transition-all duration-300 ease-in-out"
                  >
                    <button
                      type="submit"
                      className={`${
                        activeTab === tab
                          ? "text-blue-400"
                          : "text-gray-400 hover:text-gray-200"
                      } relative`}
                    >
                      {tab.charAt(0).toUpperCase() + tab.slice(1)}
                      {tab === "receive" && wasm.incomingFiles.length > 0 && (
                        <span className="absolute top-[1px] -right-6 bg-blue-500 text-white text-xs rounded-full h-5 w-5 flex items-center justify-center pt-[3px]">
                          {wasm.incomingFiles.length}
                        </span>
                      )}
                    </button>
                  </Link>
                ))}
                <div
                  className="absolute bottom-0 left-0 h-0.5 bg-blue-400 transition-all duration-300 ease-in-out"
                  style={{
                    width: "33.333%",
                    transform: `translateX(${
                      ["send", "receive", "access"].indexOf(activeTab) * 100
                    }%)`,
                  }}
                />
              </div>

              <div className="p-6 space-y-4">{children}</div>
            </div>

            {/* <div className="bg-gray-800 rounded-lg shadow-xl overflow-hidden border border-gray-700 mt-4 p-6 space-y-2">
              <span>Connected peers</span>
              <div className="flex">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-[100px]">Name</TableHead>
                      <TableHead>Wireguard IP</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {wasm.peers?.map((peer) => (
                      <TableRow key={peer.id}>
                        <TableCell className="font-medium">{peer.name}</TableCell>
                        <TableCell>{peer.ip}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div> */}
          </div>
        </main>

        <footer className="border-t border-gray-800 p-4 mt-auto bg-gray-950">
          <div className="text-center text-gray-500">Made by Coder</div>
        </footer>

        <Toaster />
      </div>
    </>
  );
}
