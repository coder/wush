import React, { useEffect } from "react";
import wasmUrl from "../assets/main.wasm";
import "../assets/wasm_exec"

export const WushContext = React.createContext<Wush | null>(null);

let wasmModule: Promise<WebAssembly.WebAssemblyInstantiatedSource>

export const createWush = async (): Promise<Wush> => {
  const go = new Go();

  if (!wasmModule) {
    wasmModule = WebAssembly.instantiateStreaming(
        fetch(wasmUrl),
        go.importObject
      );
  }
  const module = await wasmModule

  return new Promise<Wush>((resolve, reject) => {
// Start the WASM module
go.run(module.instance).then(() => {
    reject("Exited immediately")
})
  })
    

  
};

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
  async function loadWasm(go: Go) {
    console.log("actually load wasm");
    const wasmModule = await WebAssembly.instantiateStreaming(
      fetch(url),
      go.importObject
    );

    go.run(wasmModule.instance).then(() => {
      console.log("wasm exited");
      setWushCtx(null);
    });

    newWush({
      onNewPeer: (peer: Peer) => void {},
      onIncomingFile: (peer, filename, sizeBytes): boolean => {
        return false;
      },
      downloadFile: async (
        peer,
        filename,
        sizeBytes,
        stream
      ): Promise<void> => {},
    }).then((wush) => {
      console.log(wush.auth_info());
      setWushCtx(wush);
    });
  }
  loadWasm(go);
  return () => {
    console.log("Disposing wasm");
    if (!go.exited) {
      exitWush();
    }
    setWushCtx(null);
  };
}, []);
