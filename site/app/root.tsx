import {
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
} from "@remix-run/react";
import type { LinksFunction, MetaFunction } from "@remix-run/node";
import { useState, useEffect } from "react";
import { WushContext } from "./context/wush";
import wasmUrl from "~/assets/main.wasm?url";
import goWasmUrl from "~/assets/wasm_exec.js?url";

import "./tailwind.css";

export const links: LinksFunction = () => [
  {
    rel: "stylesheet",
    href: "https://fonts.googleapis.com/css2?family=Fira+Code:wght@300;400;500;600;700&display=swap",
    crossOrigin: "anonymous",
  },
  { rel: "preconnect", href: "https://fonts.googleapis.com" },
  {
    rel: "preconnect",
    href: "https://fonts.gstatic.com",
    crossOrigin: "anonymous",
  },
  {
    rel: "stylesheet",
    href: "https://fonts.googleapis.com/css2?family=Inter:ital,opsz,wght@0,14..32,100..900;1,14..32,100..900&display=swap",
  },
];

export const meta: MetaFunction = () => {
  return [
    { title: "$ wush" },
    { name: "description", content: "wush - share terminals in the browser" },
  ];
};

export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className="dracula bg-bg text-fg">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
        <script src={goWasmUrl} />
      </head>
      <body>
        {children}
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export function HydrateFallback() {
  return <p>Loading...</p>;
}

export default function App() {
  const [wushInitialized, setWushInitialized] = useState<boolean>(false);

  useEffect(() => {
    // Check if not running on the client-side
    if (typeof window === "undefined") {
      return;
    }

    const url =
      process.env.NODE_ENV === "development"
        ? wasmUrl
        : `https://storage.googleapis.com/wush-assets-prod${wasmUrl}.gz`;

    console.log("loading wasm");
    const go = new Go();
    async function loadWasm(go: Go) {
      console.log("actually load wasm");
      const wasmModule = await WebAssembly.instantiateStreaming(
        fetch(url),
        go.importObject
      );
      go.run(wasmModule.instance).then(() => {
        console.log("wasm exited");
        setWushInitialized(false);
      });
      setWushInitialized(true);
    }
    loadWasm(go);
    return () => {
      console.log("Disposing wasm");
      if (!go.exited) {
        exitWush();
      }
      setWushInitialized(false);
    };
  }, []);

  return (
    <div className="h-screen bg-gradient-to-br from-bg via-purple/5 to-bg text-fg font-mono overflow-hidden">
      <WushContext.Provider value={wushInitialized}>
        <Outlet />
      </WushContext.Provider>
    </div>
  );
}
