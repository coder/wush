import { Copy } from "lucide-react";
import { useWasm } from "@/context/wush";
import { toast } from "sonner";
import { Progress } from "@/components/ui/progress";

export default function Component() {
  const wasm = useWasm();

  const handleCopyAuthKey = () => {
    navigator.clipboard.writeText(
      `https://wush.dev#${wasm.wush.current?.auth_info().auth_key || ""}`
    );
    toast.info("Successfully copied auth key", {
      duration: 1500,
    });
  };

  return (
    <div className="space-y-4">
      <p className="text-sm text-gray-300">
        Share this authentication key to receive a file. Use{" "}
        <a href="https://github.com/coder/wush">
          <code className="bg-gray-900 px-1.5 py-0.5 rounded text-sm">
            wush cp {"<file name>"}
          </code>
        </a>{" "}
        or{" "}
        <a href="https://wush.dev/send">
          <code className="bg-gray-900 px-1.5 py-0.5 rounded text-sm">
            https://wush.dev/send
          </code>
        </a>{" "}
        to send a file to this machine.
      </p>

      <div className="flex">
        <input
          type="text"
          readOnly
          value={wasm.wush.current?.auth_info().auth_key || ""}
          className="flex-grow p-3 border border-gray-600 rounded-l bg-gray-700 text-gray-200"
        />
        <button
          type="submit"
          onClick={handleCopyAuthKey}
          className="p-3 bg-blue-600 rounded-r hover:bg-blue-700 transition-colors"
        >
          <Copy className="h-5 w-5" />
        </button>
      </div>

      {wasm.incomingFiles.map((file) => (
        <div key={file.id} className="flex flex-col space-y-2 w-full">
          <div className="flex items-center justify-between">
            <span className="text-gray-200">{file.filename}</span>
            <div className="flex items-center gap-2">
              <span className="text-gray-400 text-sm">
                {formatSpeed(file.bytesPerSecond)} •{" "}
                {formatETA(
                  (file.sizeBytes - (file.sizeBytes * file.progress) / 100) /
                    file.bytesPerSecond
                )}
              </span>
              <button
                type="button"
                onClick={() => file.close()}
                className="p-1 hover:bg-gray-700 rounded transition-colors"
              >
                ×
              </button>
            </div>
          </div>
          <Progress
            className="w-full bg-gray-700 rounded h-2 relative"
            value={file.progress}
          />
        </div>
      ))}
    </div>
  );
}

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
