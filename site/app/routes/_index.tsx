import { Link } from "@remix-run/react";
import type React from "react";
import { useState, useContext } from "react";
import { WushContext } from "~/context/wush";

export default function Component() {
  const [peerAuth, setPeerAuth] = useState("");
  const handleConnect = (e: React.FormEvent) => {
    e.preventDefault();
  };
  const wush = useContext(WushContext);

  return (
    wush && (
      <div className="flex items-center justify-center h-full">
        <form onSubmit={handleConnect} className="w-full max-w-md">
          <div className="flex flex-col items-center">
            <h1 className="text-4xl mb-8 text-[#ff79c6] italic">$ wush</h1>
            <h3>{wush.auth_info().auth_key}</h3>
            <input
              type="text"
              value={peerAuth}
              onChange={(e) => setPeerAuth(e.target.value)}
              placeholder="Enter auth key"
              className="w-full px-4 py-2 rounded bg-[#44475a] text-[#f8f8f2] placeholder-[#6272a4] focus:outline-none focus:ring-2 focus:ring-[#bd93f9]"
            />

            {/* <Link to={`/connect#${peerAuth}`}> */}
            <button
              type="submit"
              className="mt-4 px-6 py-2 border-2 border-green text-green rounded hover:bg-green hover:text-bg focus:bg-green focus:text-bg focus:ring-2 focus:ring-green transition duration-150 ease-in-out shadow-md hover:shadow-lg active:shadow-sm inline-flex items-center"
              onClick={() => {
                (async () => {
                  try {
                    const peer = await wush.connect(peerAuth);
                    console.log("got peer", peer);
                    const stream: ReadableStream<Uint8Array> =
                      new ReadableStream({
                        start(controller) {
                          const str: string = "Hello, World!";
                          const encoder = new TextEncoder(); // UTF-8 encoding by default
                          const uint8Array: Uint8Array = encoder.encode(str);
                          // This method is called when the stream is first accessed
                          controller.enqueue(uint8Array);
                          controller.close(); // Signal the end of the stream
                        },
                      });
                    await wush.transfer(peer, "hi", 13, stream, readStreamToGo);
                    console.log("transfer succeeded");
                  } catch (err) {
                    console.log("transfer failed", err);
                  }
                })();
                // wush.connect(peerAuth).then((peer) => {
                //   console.log("got peer", peer);
                //   const stream: ReadableStream<Uint8Array> = new ReadableStream(
                //     {
                //       start(controller) {
                //         const str: string = "Hello, World!";
                //         const encoder = new TextEncoder(); // UTF-8 encoding by default
                //         const uint8Array: Uint8Array = encoder.encode(str);
                //         // This method is called when the stream is first accessed
                //         controller.enqueue(uint8Array);
                //         controller.close(); // Signal the end of the stream
                //       },
                //     }
                //   );
                //   wush
                //     .transfer(peer, "hi", 13, stream, readStreamToGo)
                //     .then(() => {
                //       console.log("transfer succeeded");
                //     })
                //     .catch((err) => {
                //       console.log("transfer failed", err);
                //     });
                // });
              }}
            >
              Connect
            </button>
            {/* </Link> */}
          </div>
        </form>
      </div>
    )
  );
}

// Assume 'stream' is your ReadableStream instance
async function readStreamToGo(
  stream: ReadableStream<Uint8Array>,
  goCallback: (bytes: Uint8Array | null) => Promise<void> // The Go callback function exposed via syscall/js
): Promise<void> {
  const reader = stream.getReader();
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        // Signal EOF to Go by passing null
        console.log("calling go callback EOF");
        goCallback(null);
        break;
      }
      if (value) {
        // Pass the chunk to Go
        console.log("calling go callback");
        await goCallback(value);
      }
    }
  } catch (error) {
    console.error("Error reading stream:", error);
    // Optionally handle errors and signal to Go
  } finally {
    reader.releaseLock();
  }
}
