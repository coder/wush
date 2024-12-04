import "../wasm/wasm_exec.js";
import wasmModule from "../wasm/main.wasm";

import type React from "react";
import type { ReactNode } from "react";
import { createContext, useContext, useEffect, useState, useRef } from "react";

const iceServers = [
  {
    urls: "stun:stun.l.google.com:19302",
  },
  ...(process.env.NEXT_PUBLIC_TURN_SERVER_URL
    ? [
        {
          urls: process.env.NEXT_PUBLIC_TURN_SERVER_URL,
          username: process.env.NEXT_PUBLIC_TURN_USERNAME ?? "",
          credential: process.env.NEXT_PUBLIC_TURN_CREDENTIAL ?? "",
          credentialType: "password",
        },
      ]
    : []),
];

interface WasmContextProps {
  wush: React.MutableRefObject<Wush | null>;
  loading: boolean;
  error?: string;

  connectedPeer?: Peer;
  peers: Peer[];

  rtc: React.MutableRefObject<Map<string, RTCPeerConnection | null>>;
  dataChannel: React.MutableRefObject<Map<string, RTCDataChannel | null>>;

  incomingFiles: IncomingFile[];
}

type IncomingFile = {
  id: number;
  peerId: string;
  filename: string;
  sizeBytes: number;
  bytesPerSecond: number;
  // 0-100
  progress: number;
  close: () => void;
};

// Define the context type as WasmModule or null initially
const WasmContext = createContext<WasmContextProps>({
  loading: true,
  peers: [],
  wush: { current: null },
  rtc: { current: new Map() },
  dataChannel: { current: new Map() },
  incomingFiles: [],
});

interface WasmProviderProps {
  children: ReactNode;
}

export const WasmProvider: React.FC<WasmProviderProps> = ({ children }) => {
  const [initOnce, setInitOnce] = useState<boolean>(false);
  const [wasm, setWasm] = useState<WasmContextProps>({
    loading: true,
    peers: [],
    wush: useRef(null),
    rtc: useRef(new Map()),
    dataChannel: useRef(new Map()),
    incomingFiles: [],
  });
  const [currentConnectedPeer, setCurrentConnectedPeer] = useState<string>("");

  const currentFragment = (() => {
    // Check if we're in the browser
    return typeof window !== "undefined"
      ? window.location.hash.substring(1)
      : "";
  })();

  const updateField = (
    fieldName: keyof WasmContextProps,
    value: WasmContextProps[keyof WasmContextProps]
  ) => {
    setWasm((prevState) => ({
      ...prevState,
      [fieldName]: value,
    }));
  };

  const config: WushConfig = {
    onNewPeer: (peer: Peer) => {
      console.log(JSON.stringify(iceServers));
      const newPeerConnection = new RTCPeerConnection({
        iceServers,
      });

      newPeerConnection.onicecandidate = (event) => {
        if (event.candidate) {
          console.log("onicecandidate", event.candidate);
          wasm.wush.current?.sendWebrtcCandidate(peer.id, event.candidate);
        }
      };

      newPeerConnection.ondatachannel = (event) => {
        event.channel.onclose = () => {
          wasm.dataChannel.current.delete(peer.id);
        };
        setupDataChannel(event.channel, setWasm);

        setWasm((prevState) => {
          prevState.dataChannel.current.set(peer.id, event.channel);

          console.log(
            "got new data channel",
            JSON.stringify(Array.from(prevState.dataChannel.current.keys()))
          );

          return {
            ...prevState,
          };
        });
      };

      wasm.rtc.current.set(peer.id, newPeerConnection);

      console.log(
        "on new peer called, setting wasm",
        JSON.stringify(Array.from(wasm.rtc.current.keys()))
      );

      setWasm((prevState) => {
        return {
          ...prevState,
          peers: [...prevState.peers, peer],
        };
      });
    },
    onIncomingFile: async (peer, filename, sizeBytes): Promise<boolean> => {
      return false;
    },
    downloadFile: async (
      peer,
      filename,
      sizeBytes,
      stream
    ): Promise<void> => {},
    onWebrtcOffer: async (
      id: string,
      offer: RTCSessionDescriptionInit
    ): Promise<RTCSessionDescription | null> => {
      const rtc = wasm.rtc.current.get(id);
      if (!rtc) {
        console.log(
          "webrtc is null",
          JSON.stringify(Array.from(wasm.rtc.current.keys()))
        );
        return null;
      }

      try {
        await rtc.setRemoteDescription(offer);
        const answer = await rtc?.createAnswer();
        await rtc.setLocalDescription(answer);
      } catch (ex) {
        console.error(`failed to create answer: ${ex}`);
      }

      return rtc.localDescription;
    },
    onWebrtcAnswer: async (
      id: string,
      answer: RTCSessionDescriptionInit
    ): Promise<void> => {
      const rtc = wasm.rtc.current.get(id);
      if (!rtc) {
        console.log(
          "webrtc is null",
          JSON.stringify(Array.from(wasm.rtc.current.keys()))
        );
        return;
      }

      try {
        await rtc.setRemoteDescription(answer);
      } catch (ex) {
        console.error(`failed to set remote desc: ${ex}`);
      }
    },
    onWebrtcCandidate: async (
      id: string,
      candidate: RTCIceCandidateInit
    ): Promise<void> => {
      const rtc = wasm.rtc.current.get(id);
      if (!rtc) {
        console.log(
          "webrtc is null???",
          JSON.stringify(Array.from(wasm.rtc.current.keys()))
        );
        return;
      }

      try {
        await rtc.addIceCandidate(candidate);
      } catch (ex) {
        console.error(`failed to add candidate: ${ex}`);
      }
    },
  };

  useEffect(() => {
    if (initOnce) {
      return;
    }
    setInitOnce(true);

    async function loadWasm() {
      const go = new Go();

      try {
        const response = await fetch(wasmModule as unknown as string);
        const buffer = await response.arrayBuffer();
        const module = await WebAssembly.instantiate(buffer, go.importObject);

        go.run(module.instance).then(() => {
          setWasm((prevState) => ({
            ...prevState,
            loading: false,
            peers: [],
            error: "WASM exited",
          }));
        });

        newWush(config)
          .then((wush) => {
            setWasm((prevState) => {
              prevState.wush.current = wush;
              return {
                ...prevState,
                loading: false,
              };
            });
          })
          .catch((ex) => {
            setWasm((prevState) => {
              prevState.wush.current = null;
              return {
                ...prevState,
                loading: false,
                peers: [],
                error: `Wush failed to initialize: ${ex}`,
              };
            });
          });
      } catch (error) {
        setWasm((prevState) => ({
          ...prevState,
          loading: false,
          error: `Failed to load WASM: ${error}`,
        }));
      }
    }

    loadWasm();
  }, [config, initOnce]);

  useEffect(() => {
    console.log("connected peer", currentFragment);
    if (!wasm.wush.current) {
      console.log("can't connect, wush not initialized");
      return;
    }

    if (currentFragment === "" && currentConnectedPeer === "") {
      return;
    }

    if (currentFragment === "" && currentConnectedPeer !== "") {
      wasm.connectedPeer?.cancel();
      updateField("connectedPeer", undefined);
      setCurrentConnectedPeer(currentFragment);
      return;
    }

    if (currentConnectedPeer === currentFragment) {
      return;
    }

    setCurrentConnectedPeer(currentFragment);
    async function connectPeer() {
      try {
        const newPeerConnection = new RTCPeerConnection({
          iceServers: iceServers,
        });

        const newDataChannel =
          newPeerConnection.createDataChannel("fileTransfer");
        setupDataChannel(newDataChannel, setWasm);

        const peerInfo = wasm.wush.current?.parseAuthKey(currentFragment);
        if (!peerInfo) {
          throw new Error("failed to parse peer id");
        }

        // Initialize an array to buffer candidates
        const bufferedCandidates: RTCIceCandidate[] = [];

        // Set up 'onicecandidate' handler before starting ICE gathering
        newPeerConnection.onicecandidate = (
          event: RTCPeerConnectionIceEvent
        ) => {
          if (event.candidate) {
            console.log("Buffering ICE candidate", event.candidate);
            bufferedCandidates.push(event.candidate);
          }
        };

        const offer = await newPeerConnection.createOffer();
        await newPeerConnection.setLocalDescription(offer);

        setWasm((prevState) => {
          prevState.rtc.current.set(peerInfo.id, newPeerConnection);
          prevState.dataChannel.current.set(peerInfo.id, newDataChannel);

          console.log(
            "connect peer called, setting wasm",
            JSON.stringify(Array.from(prevState.rtc.current.keys()))
          );

          return {
            ...prevState,
          };
        });

        const peer = await wasm.wush.current?.connect(currentFragment, offer);
        if (!peer) {
          throw new Error("Failed to connect to peer: peer is null");
        }

        newPeerConnection.onicecandidate = (
          event: RTCPeerConnectionIceEvent
        ) => {
          if (event.candidate) {
            console.log("onicecandidate", event.candidate);
            wasm.wush.current?.sendWebrtcCandidate(
              peerInfo.id,
              event.candidate
            );
          }
        };

        // Add a method to send buffered candidates
        for (const candidate of bufferedCandidates) {
          wasm.wush.current?.sendWebrtcCandidate(peerInfo.id, candidate);
        }
        // Clear the buffer after sending
        bufferedCandidates.length = 0;

        setWasm((prevState) => {
          return {
            ...prevState,
            connectedPeer: peer,
          };
        });
      } catch (error) {
        updateField("error", `Failed to connect to peer: ${error}`);
      }
    }

    connectPeer();
  }, [wasm, currentFragment, updateField, currentConnectedPeer]);

  return <WasmContext.Provider value={wasm}>{children}</WasmContext.Provider>;
};

// Custom hook to use the WASM module in components
export function useWasm() {
  const context = useContext(WasmContext);
  if (!context) {
    throw new Error("useWasm must be used within a WasmProvider");
  }
  return context;
}

const setupDataChannel = (
  dataChannel: RTCDataChannel,
  setWasm: React.Dispatch<React.SetStateAction<WasmContextProps>>
) => {
  dataChannel.binaryType = "arraybuffer";

  let receivedBuffers: ArrayBuffer[] = [];
  let expectedFileSize = 0;
  let receivedFileName = "";
  let startTime = 0;
  let fileId = 0;

  dataChannel.onopen = () => {
    console.log(
      "Data channel opened, label:",
      dataChannel.label,
      "id:",
      dataChannel.id
    );
  };

  dataChannel.onerror = (error) => {
    console.error("Data channel error:", error);
  };

  dataChannel.onmessage = (event) => {
    if (typeof event.data === "string") {
      const message = JSON.parse(event.data) as RtcMetadata;
      if (message.type === "file_metadata") {
        expectedFileSize = message.fileMetadata.fileSize;
        receivedFileName = message.fileMetadata.fileName;
        receivedBuffers = [];
        startTime = performance.now();
        fileId = Date.now();

        setWasm((prev) => ({
          ...prev,
          incomingFiles: [
            ...prev.incomingFiles,
            {
              id: fileId,
              peerId: "test",
              filename: receivedFileName,
              sizeBytes: expectedFileSize,
              bytesPerSecond: 0,
              progress: 0,
              close: () => {
                setWasm((prev) => ({
                  ...prev,
                  incomingFiles: prev.incomingFiles.filter(
                    (f) => f.id !== fileId
                  ),
                }));
              },
            },
          ],
        }));
      } else if (message.type === "file_complete") {
        const receivedFile = new Blob(receivedBuffers);
        triggerFileDownload(receivedFile, receivedFileName);
        receivedBuffers = [];

        // Update progress to 100% but don't remove
        setWasm((prev) => ({
          ...prev,
          incomingFiles: prev.incomingFiles.map((file) =>
            file.id === fileId ? { ...file, progress: 100 } : file
          ),
        }));

        console.log("sending file ack...");
        const ackMessage: RtcMetadata = {
          type: "file_ack",
          fileMetadata: { fileName: "", fileSize: 0 },
        };
        try {
          dataChannel.send(JSON.stringify(ackMessage));
          console.log("file ack sent successfully");
        } catch (err) {
          console.error("Error sending ack:", err);
        }
      }
    } else if (event.data instanceof ArrayBuffer) {
      receivedBuffers.push(event.data);
      const receivedSize = receivedBuffers.reduce(
        (acc, buffer) => acc + buffer.byteLength,
        0
      );

      const now = performance.now();
      const progressPercent = (receivedSize / expectedFileSize) * 100;
      const currentSpeed = receivedSize / ((now - startTime) / 1000);

      setWasm((prev) => ({
        ...prev,
        incomingFiles: prev.incomingFiles.map((file) =>
          file.id === fileId
            ? {
                ...file,
                progress: progressPercent,
                bytesPerSecond: currentSpeed,
              }
            : file
        ),
      }));
    }
  };
};

export type RtcMetadata = {
  type: "file_metadata" | "file_complete" | "file_ack";
  fileMetadata: {
    fileName: string;
    fileSize: number;
  };
};

const triggerFileDownload = (blob: Blob, fileName: string) => {
  const url = URL.createObjectURL(blob);

  // Create a temporary anchor element and trigger the download
  const a = document.createElement("a");
  a.href = url;
  a.download = fileName;
  document.body.appendChild(a);
  a.style.display = "none";
  a.click();
  a.remove();

  // Release the object URL after a short delay
  setTimeout(() => URL.revokeObjectURL(url), 100);
};
