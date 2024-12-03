import { useWasm } from "@/context/wush";
import type { RtcMetadata } from "@/context/wush";
import { FileUp, Info, X } from "lucide-react";
import { useState, useRef, useEffect } from "react";
import { Progress } from "@/components/ui/progress";
import Link from "next/link";
import { toast } from "sonner";

const MAX_BUFFER_SIZE = 16 * 1024 * 1024; // 16 MB
const CHUNK_SIZE = 16 * 1024; // 64 KB

const FileTransfer: React.FC = () => {
  const wasm = useWasm();
  const [progress, setProgress] = useState(0);
  const [file, setFile] = useState<File | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const currentFragment = (() => {
    // Check if we're in the browser
    return typeof window !== "undefined"
      ? window.location.hash.substring(1)
      : "";
  })();

  const sendStartTime = useRef<number | null>(null);

  const isSubmitDisabled = !wasm.wush || !wasm.connectedPeer || !file;

  const handleFileInput = (event: React.ChangeEvent<HTMLInputElement>) => {
    if (event.target.files?.[0]) {
      setFile(event.target.files[0]);
    }
  };

  const [transferSpeed, setTransferSpeed] = useState<number>(0);
  const speedSamplesRef = useRef<{ time: number; bytes: number }[]>([]);
  const SPEED_WINDOW_MS = 2000; // 2 second window for averaging

  const [isDragging, setIsDragging] = useState(false);
  const [isTransferring, setIsTransferring] = useState(false);
  const [showProgress, setShowProgress] = useState(false);
  const [estimatedTimeRemaining, setEstimatedTimeRemaining] =
    useState<string>("");

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

  const sendFile = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setIsTransferring(true);
    setShowProgress(true);

    if (!file) {
      console.error("No file selected");
      return;
    }

    if (!wasm.rtc.current || !wasm.connectedPeer) {
      console.error("No peer");
      return;
    }

    const rtc = wasm.rtc.current.get(wasm.connectedPeer.id);
    if (!rtc || rtc.connectionState !== "connected") {
      console.error("No peer");
      return;
    }

    const dc = rtc.createDataChannel(
      `file_transfer_${file.name}_${Date.now()}`
    );

    // Wait for data channel to open before proceeding
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error("Data channel open timeout"));
      }, 10000); // 10 second timeout

      dc.onopen = () => {
        clearTimeout(timeout);
        resolve();
      };

      dc.onerror = (err) => {
        clearTimeout(timeout);
        reject(err);
      };
    });

    console.log("Sending file metadata");
    // Send file metadata first
    const fileMetadata = {
      fileName: file.name,
      fileSize: file.size,
    };
    dc.send(
      JSON.stringify({
        type: "file_metadata",
        fileMetadata: fileMetadata,
      } as RtcMetadata)
    );
    console.log("Sent file metadata");

    dc.bufferedAmountLowThreshold = 65536; // 64 KB

    let offset = 0;

    sendStartTime.current = performance.now(); // Start timing

    const fileReader = new FileReader();

    fileReader.onerror = (error) => {
      console.error("File reading error:", error);
    };

    const sendNextChunk = () => {
      if (offset >= file.size) {
        return;
      }

      const slice = file.slice(offset, offset + CHUNK_SIZE);
      fileReader.readAsArrayBuffer(slice);
    };

    let lastUpdate = performance.now();
    speedSamplesRef.current = []; // Reset samples when starting new transfer

    fileReader.onload = () => {
      if (!fileReader.result) {
        console.error("FileReader result is null");
        return;
      }

      const chunk = fileReader.result as ArrayBuffer;

      const canSend = () =>
        dc.bufferedAmount + chunk.byteLength < MAX_BUFFER_SIZE;

      const sendChunk = () => {
        dc.send(chunk);
        offset += chunk.byteLength;

        // Update speed calculation with moving average
        const now = performance.now();
        const timeDiff = now - lastUpdate;
        if (timeDiff >= 100) {
          // Still sample every 100ms
          // Add new sample
          speedSamplesRef.current.push({
            time: now,
            bytes: offset,
          });

          // Remove samples older than our window
          const windowStart = now - SPEED_WINDOW_MS;
          while (
            speedSamplesRef.current.length > 1 &&
            speedSamplesRef.current[0].time < windowStart
          ) {
            speedSamplesRef.current.shift();
          }

          // Calculate average speed over the window
          if (speedSamplesRef.current.length > 1) {
            const oldestSample = speedSamplesRef.current[0];
            const bytesDiff = offset - oldestSample.bytes;
            const timeDiffSecs = (now - oldestSample.time) / 1000;
            const speedMBps = bytesDiff / timeDiffSecs / (1024 * 1024);
            setTransferSpeed(speedMBps);

            // Calculate ETA
            const remainingBytes = file.size - offset;
            const remainingSeconds = remainingBytes / (speedMBps * 1024 * 1024);

            let etaText = "";
            if (remainingSeconds < 60) {
              etaText = `${Math.ceil(remainingSeconds)}s`;
            } else {
              const minutes = Math.floor(remainingSeconds / 60);
              const seconds = Math.ceil(remainingSeconds % 60);
              etaText = `${minutes}m ${seconds}s`;
            }
            setEstimatedTimeRemaining(etaText);
          }

          lastUpdate = now;
        }

        setProgress(Math.round((offset / file.size) * 100));

        if (offset >= file.size) {
          // Transfer complete
          setIsTransferring(false);
          dc.close();

          // Calculate final transfer statistics
          const endTime = performance.now();
          const totalSeconds = sendStartTime.current
            ? (endTime - sendStartTime.current) / 1000
            : 0;
          const averageSpeed = file.size / (1024 * 1024) / totalSeconds;

          // Show completion toast using Sonner
          toast.success("Transfer Complete", {
            description: `File transferred in ${totalSeconds.toFixed(
              1
            )}s at ${averageSpeed.toFixed(1)} MB/s`,
            duration: 5000,
          });

          // Start fade out timer
          setTimeout(() => {
            setShowProgress(false);
            setProgress(0);
            setTransferSpeed(0);
          }, 2000);
        } else {
          // Proceed to next chunk
          sendNextChunk();
        }
      };

      if (canSend()) {
        sendChunk();
      } else {
        // Wait for 'bufferedamountlow' event
        const onBufferedAmountLow = () => {
          dc.removeEventListener("bufferedamountlow", onBufferedAmountLow);
          // Re-check if we can send now
          if (canSend()) {
            sendChunk();
          } else {
            // Should not happen, but just in case
            console.error(
              "Buffered amount still too high after 'bufferedamountlow' event"
            );
          }
        };
        dc.addEventListener("bufferedamountlow", onBufferedAmountLow);
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

  return (
    <form onSubmit={sendFile} className="space-y-4">
      {/* <p className="text-sm text-gray-300">
        Open&nbsp;
        <Link href={`/receive#${currentFragment}`}>receive</Link> or run{" "}
        <code>wush serve</code> to obtain a key. Paste it here to securely send
        a file.
      </p> */}
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
      {showProgress && (
        <div
          className="transition-opacity duration-500 ease-in-out"
          style={{ opacity: showProgress ? "1" : "0" }}
        >
          <Progress
            value={progress}
            className="w-full bg-gray-700 rounded h-6 relative"
          >
            <div className="absolute inset-0 flex items-center justify-center text-xs text-white leading-none">
              {transferSpeed.toFixed(1)} MB/s{" "}
              {estimatedTimeRemaining &&
                `• ${estimatedTimeRemaining} remaining`}
            </div>
          </Progress>
        </div>
      )}
    </form>
  );
};

export default FileTransfer;
