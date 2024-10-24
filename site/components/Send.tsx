import React, { useState, useRef } from "react";
import { FileUp, Info } from "lucide-react";
import Link from "next/link";

const Send: React.FC<{
  readonly wush?: Wush;
}> = (props) => {
  const [authKey, setAuthKey] = useState("");
  const [fileName, setFileName] = useState("");
  const [progress, setProgress] = useState(0);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const submit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!props.wush) {
      return;
    }
    const file = fileInputRef.current?.files?.[0];
    if (!file) {
      return;
    }

    console.log("File size:", file.size);

    try {
      const startTime = performance.now();
      const peer = await props.wush.connect(authKey);
      console.log("Connected in", performance.now() - startTime, "ms");

      let lastProgressUpdate = 0;
      const transferStartTime = performance.now();
      await props.wush.transfer(
        peer,
        file.name,
        file.size,
        file.stream(),
        (bytesRead) => {
          // Update progress less frequently
            const percentage = Math.round((bytesRead / file.size) * 100);
            setProgress(percentage);
            
            const now = performance.now();
            const elapsedSeconds = (now - transferStartTime) / 1000;
            const speedMBps = (bytesRead / 1024 / 1024) / elapsedSeconds;
            console.log(`Transfer progress: ${percentage}%, Speed: ${speedMBps.toFixed(2)} MB/s`);
            
            lastProgressUpdate = bytesRead;
        }
      );

      const totalDuration = (performance.now() - startTime) / 1000;
      const averageSpeed = (file.size / 1024 / 1024) / totalDuration;
      console.log(`Transfer completed in ${totalDuration.toFixed(2)} seconds`);
      console.log(`Average speed: ${averageSpeed.toFixed(2)} MB/s`);

      setProgress(100);
    } catch (error) {
      console.error("Transfer error:", error);
    }
  };

  const isSubmitDisabled = !props.wush || !authKey || !fileName;

  return (
    <form onSubmit={submit} className="space-y-4">
      <p className="text-sm text-gray-300">
        Open&nbsp;
        <Link href={`https://wush.coder.com/receive`} target="_blank">
          receive
        </Link>{" "}
        or run <code>wush serve</code> to obtain a key. Paste it here to
        securely send a file.
      </p>
      <div className="relative">
        <input
          type="file"
          ref={fileInputRef}
          className="hidden"
          onChange={(e) => setFileName(e.target.files?.[0]?.name || "")}
        />
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          className="w-full p-3 border border-gray-600 rounded bg-gray-700 text-gray-200 text-left"
        >
          {fileName || "Choose a file"}
        </button>
        <FileUp className="absolute right-3 top-1/2 transform -translate-y-1/2 h-5 w-5 text-gray-400" />
      </div>
      <input
        type="text"
        value={authKey}
        onChange={(e) => setAuthKey(e.target.value)}
        placeholder="Enter auth key"
        className="w-full p-3 border border-gray-600 rounded bg-gray-700 text-gray-200"
      />
      <div className="relative">
        <button
          type="submit"
          className={`w-full p-3 bg-blue-600 rounded flex items-center justify-center transition-colors ${
            isSubmitDisabled
              ? "opacity-50 cursor-not-allowed"
              : "hover:bg-blue-700"
          }`}
          disabled={isSubmitDisabled}
        >
          <FileUp className="mr-2 h-5 w-5" />
          Send
        </button>
        {!props.wush && (
          <div className="absolute -top-8 left-1/2 transform -translate-x-1/2 bg-gray-800 text-white text-xs rounded py-1 px-2 whitespace-nowrap opacity-0 group-hover:opacity-100 transition-opacity">
            <Info className="inline-block mr-1 h-3 w-3" />
            Wush is initializing
          </div>
        )}
      </div>
      {progress > 0 && (
        <div className="w-full bg-gray-200 rounded-full h-2.5 dark:bg-gray-700">
          <div
            className="bg-blue-600 h-2.5 rounded-full"
            style={{ width: `${progress}%` }}
          ></div>
        </div>
      )}
    </form>
  );
};

export default Send;
