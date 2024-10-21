import "../public/wasm_exec.js";

export const createWush = async (
  config: WushConfig = {
    onNewPeer: (peer: Peer) => void {},
    onIncomingFile: async (peer, filename, sizeBytes): Promise<boolean> => {
      return false;
    },
    downloadFile: async (
      peer,
      filename,
      sizeBytes,
      stream
    ): Promise<void> => {},
  }
): Promise<Wush> => {
  const go = new Go();

  const module = await WebAssembly.instantiateStreaming(
    fetch("/main.wasm"),
    go.importObject
  );

  return new Promise<Wush>(async (resolve, reject) => {
    // Start the WASM module
    go.run(module.instance).then(() => {
      reject("Exited immediately");
    });

    try {
      const wush = await newWush(config);

      resolve({
        ...wush,
        stop: () => {
          wush.stop();
          go.exit(0);
        },
      });
    } catch (ex) {
      reject(ex);
    }
  });
};
