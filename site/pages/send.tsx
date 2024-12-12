import { useWasm } from "@/context/wush";
import type { FileTransferState, RtcMetadata } from "@/context/wush";
import { FileUp, Info, X } from "lucide-react";
import { useState, useRef, useEffect, useCallback } from "react";
import { Progress } from "@/components/ui/progress";

const MAX_BUFFER_SIZE = 16 * 1024 * 1024; // 16 MB
const CHUNK_SIZE = 16 * 1024; // 64 KB
const SPEED_WINDOW_MS = 2000; // 2 second window for averaging

const formatSpeed = (bytesPerSecond: number): string => {
  let speed = bytesPerSecond;
  let unit = "B/s";

  if (speed > 1024) {
    speed /= 1024;
    unit = "KB/s";
  }
  if (speed > 1024) {
    speed /= 1024;
    unit = "MB/s";
  }

  return `${speed.toFixed(2)} ${unit}`;
};

const formatETA = (seconds: number): string => {
  if (seconds === Number.POSITIVE_INFINITY || Number.isNaN(seconds))
    return "calculating...";
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = Math.round(seconds % 60);
  return `${minutes}m ${remainingSeconds}s`;
};

type RTCStatsReport = {
  bytesSent?: number;
  timestamp: number;
};

const FileTransfer: React.FC = () => {
  const wasm = useWasm();
  const { activeTransfers, setActiveTransfers } = wasm;
  const [file, setFile] = useState<File | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const isSubmitDisabled = !wasm.wush || !wasm.connectedPeer || !file;

  const handleFileInput = (event: React.ChangeEvent<HTMLInputElement>) => {
    if (event.target.files?.[0]) {
      setFile(event.target.files[0]);
    }
  };

  const [isDragging, setIsDragging] = useState(false);
  const [isTransferring, setIsTransferring] = useState(false);

  const handleDragOver = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setIsDragging(true);
  };

  const handleDragLeave = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setIsDragging(false);
  };

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setIsDragging(false);

    if (e.dataTransfer.files?.[0]) {
      setFile(e.dataTransfer.files[0]);
    }
  };

  const dataChannelRef = useRef<RTCDataChannel | null>(null);
  const statsIntervalRef = useRef<NodeJS.Timeout>();
  const lastStatsRef = useRef<RTCStatsReport>();

  const cleanupStatsInterval = useCallback(() => {
    if (statsIntervalRef.current) {
      setIsTransferring(false);
      clearInterval(statsIntervalRef.current);
      statsIntervalRef.current = undefined;
    }
  }, []);

  const sendFile = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!file) return;

    if (!wasm.rtc.current || !wasm.connectedPeer) {
      console.error("No peer");
      return;
    }

    const rtc = wasm.rtc.current.get(wasm.connectedPeer.id);
    if (!rtc || rtc.connectionState !== "connected") {
      console.error("No peer");
      return;
    }

    const transferId = `file_transfer_${file.name}_${Date.now()}`;
    const dc = rtc.createDataChannel(transferId);
    dataChannelRef.current = dc;
    const startingStats = await rtc.getStats();
    let startingBytesSent = 0;
    // biome-ignore lint/complexity/noForEach: the api requires this
    startingStats.forEach((report) => {
      if (report.type === "transport") {
        startingBytesSent = report.bytesSent || 0;
      }
    });

    // Create new transfer state
    const newTransfer: FileTransferState = {
      id: transferId,
      file,
      progress: 0,
      bytesPerSecond: 0,
      eta: "calculating...",
      dc,
      completed: false,
    };

    setActiveTransfers((prev) => [...prev, newTransfer]);
    setFile(null); // Clear the file input
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    setIsTransferring(true);

    // Setup stats monitoring
    cleanupStatsInterval();
    statsIntervalRef.current = setInterval(async () => {
      const stats = await rtc.getStats();
      let dataChannelReport: RTCStatsReport | undefined;

      // biome-ignore lint/complexity/noForEach: the api requires this
      stats.forEach((report) => {
        if (report.type === "transport") {
          dataChannelReport = report;
        }
      });

      if (!dataChannelReport) {
        console.log("no data channel report");
        return;
      }

      const bytesSent = dataChannelReport.bytesSent || 0;
      const timestamp = dataChannelReport.timestamp;

      if (lastStatsRef.current) {
        const bytesDiff = bytesSent - (lastStatsRef.current.bytesSent || 0);
        const timeDiff = (timestamp - lastStatsRef.current.timestamp) / 1000;

        if (timeDiff > 0 && bytesDiff >= 0) {
          const bytesPerSecond = bytesDiff / timeDiff;
          const progress = Math.round(
            ((bytesSent - startingBytesSent) / file.size) * 100
          );
          const remainingBytes = file.size - bytesSent;
          const eta = formatETA(remainingBytes / bytesPerSecond);

          setActiveTransfers((prev) =>
            prev.map((t) =>
              t.id === transferId ? { ...t, progress, bytesPerSecond, eta } : t
            )
          );
        }
      }

      lastStatsRef.current = { bytesSent, timestamp };
    }, 1000);

    // Wait for data channel to open
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(
        () => reject(new Error("Data channel open timeout")),
        10000
      );
      dc.onopen = () => {
        clearTimeout(timeout);
        resolve();
      };
      dc.onerror = (err) => {
        clearTimeout(timeout);
        reject(err);
      };
    });

    // Send file metadata
    const fileMetadata = {
      fileName: file.name,
      fileSize: file.size,
    };
    dc.send(
      JSON.stringify({ type: "file_metadata", fileMetadata } as RtcMetadata)
    );

    dc.bufferedAmountLowThreshold = 65536;
    let offset = 0;
    const startTime = performance.now();

    const fileReader = new FileReader();
    fileReader.onerror = (error) => console.error("File reading error:", error);

    const sendNextChunk = () => {
      if (offset >= file.size) {
        return;
      }

      const slice = file.slice(offset, offset + CHUNK_SIZE);
      fileReader.readAsArrayBuffer(slice);
    };

    fileReader.onload = () => {
      if (!fileReader.result) {
        console.error("FileReader result is null");
        return;
      }

      const chunk = fileReader.result as ArrayBuffer;
      const canSend = () =>
        dc.readyState === "open" &&
        dc.bufferedAmount + chunk.byteLength < MAX_BUFFER_SIZE;

      const sendChunk = () => {
        try {
          if (dc.readyState !== "open") {
            console.log("Data channel no longer open, stopping transfer");
            cleanupStatsInterval();
            setActiveTransfers((prev) =>
              prev.filter((t) => t.id !== transferId)
            );
            fileReader.abort();
            return;
          }

          dc.send(chunk);
          offset += chunk.byteLength;

          if (offset >= file.size) {
            cleanupStatsInterval();
            const endTime = performance.now();
            const totalSeconds = (endTime - startTime) / 1000;

            // Get final stats
            rtc.getStats().then((stats) => {
              const averageSpeed = file.size / totalSeconds;

              setActiveTransfers((prev) =>
                prev.map((t) =>
                  t.id === transferId
                    ? {
                        ...t,
                        progress: 100,
                        completed: true,
                        bytesPerSecond: averageSpeed,
                        eta: `Completed in ${totalSeconds.toFixed(1)}s`,
                        finalStats: {
                          duration: totalSeconds,
                          averageSpeed: averageSpeed / (1024 * 1024), // Convert to MB/s
                        },
                      }
                    : t
                )
              );
            });

            dc.close();
          } else {
            // Proceed to next chunk
            sendNextChunk();
          }
        } catch (error) {
          console.error("Error sending chunk:", error);
          cleanupStatsInterval();
          setActiveTransfers((prev) => prev.filter((t) => t.id !== transferId));
        }
      };

      if (canSend()) {
        sendChunk();
      } else if (dc.readyState === "open") {
        // Only set up bufferedamountlow listener if channel is still open
        const onBufferedAmountLow = () => {
          dc.removeEventListener("bufferedamountlow", onBufferedAmountLow);
          if (canSend()) {
            sendChunk();
          } else {
            console.error(
              "Buffered amount still too high after 'bufferedamountlow' event"
            );
          }
        };
        dc.addEventListener("bufferedamountlow", onBufferedAmountLow);
      } else {
        console.log("Data channel no longer open, stopping transfer");
        cleanupStatsInterval();
        setActiveTransfers((prev) => prev.filter((t) => t.id !== transferId));
        fileReader.abort();
      }
    };

    // Start sending
    sendNextChunk();
  };

  const clearFile = (e: React.MouseEvent) => {
    e.stopPropagation(); // Prevent triggering the file input click
    setFile(null);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  };

  const cancelTransfer = (transferId: string) => {
    const transfer = activeTransfers.find((t) => t.id === transferId);
    if (transfer) {
      cleanupStatsInterval();
      dataChannelRef.current = null;
      try {
        console.log("Closing data channel");
        transfer.dc.close();
      } catch (error) {
        console.error("Error closing data channel:", error);
      }
      setActiveTransfers((prev) => prev.filter((t) => t.id !== transferId));
    }
  };

  return (
    <form onSubmit={sendFile} className="space-y-4">
      <p className="text-sm text-gray-300">
        Run{" "}
        <a href="https://github.com/coder/wush">
          <code className="bg-gray-900 px-1.5 py-0.5 rounded text-sm">
            wush serve
          </code>
        </a>{" "}
        or open{" "}
        <a href="https://wush.dev/receive">
          <code className="bg-gray-900 px-1.5 py-0.5 rounded text-sm">
            https://wush.dev/receive
          </code>
        </a>{" "}
        on another machine to obtain a key. Paste it above to securely send a
        file.
      </p>
      <div
        className="relative"
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        <input
          type="file"
          ref={fileInputRef}
          className="hidden"
          onChange={handleFileInput}
        />
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          className={`w-full p-3 border border-gray-600 rounded bg-gray-700 text-gray-200 text-left transition-colors ${
            isDragging ? "border-blue-500 bg-gray-600" : ""
          }`}
        >
          {file?.name || (isDragging ? "Drop file here" : "Choose a file")}
          {file ? (
            <button
              onClick={clearFile}
              className="absolute right-3 top-1/2 transform -translate-y-1/2 h-5 w-5 text-gray-400 hover:text-gray-200 transition-colors"
              type="button"
            >
              <X className="h-5 w-5" />
            </button>
          ) : (
            <FileUp className="absolute right-3 top-1/2 transform -translate-y-1/2 h-5 w-5 text-gray-400" />
          )}
        </button>
      </div>
      <div className="relative">
        <button
          type="submit"
          className={`w-full p-3 bg-blue-600 rounded flex items-center justify-center transition-colors ${
            isSubmitDisabled || isTransferring
              ? "opacity-50 cursor-not-allowed"
              : "hover:bg-blue-700"
          }`}
          disabled={isSubmitDisabled || isTransferring}
        >
          <FileUp className="mr-2 h-5 w-5" />
          {isTransferring ? "Sending..." : "Send"}
        </button>
        {!wasm.wush && (
          <div className="absolute -top-8 left-1/2 transform -translate-x-1/2 bg-gray-800 text-white text-xs rounded py-1 px-2 whitespace-nowrap opacity-0 group-hover:opacity-100 transition-opacity">
            <Info className="inline-block mr-1 h-3 w-3" />
            Wush is initializing
          </div>
        )}
      </div>
      {activeTransfers.map((transfer) => (
        <div key={transfer.id} className="flex flex-col space-y-2 w-full">
          <div className="flex items-center justify-between">
            <span className="text-gray-200">{transfer.file.name}</span>
            <div className="flex items-center gap-2">
              <span className="text-gray-400 text-sm">
                {formatSpeed(transfer.bytesPerSecond)} • {transfer.eta}
              </span>
              <button
                type="button"
                onClick={() => cancelTransfer(transfer.id)}
                className="p-1 hover:bg-gray-700 rounded transition-colors"
              >
                ×
              </button>
            </div>
          </div>
          <Progress
            value={transfer.progress}
            className="w-full bg-gray-700 rounded h-2 relative"
          />
        </div>
      ))}
    </form>
  );
};

export default FileTransfer;
