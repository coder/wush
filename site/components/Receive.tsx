import { Copy } from "lucide-react";

export interface IncomingFile {
  peer: Peer;
  filename: string;
  sizeBytes: number;

  download: () => Promise<void>;
  cancel: () => Promise<void>;

  downloading: boolean;
  bytesDownloaded: number;
};

const Receive: React.FC<{ wush?: Wush }> = ({ wush }) => {
  const handleCopyAuthKey = () => {
    navigator.clipboard.writeText(wush?.auth_info().auth_key || "");
  };

  return (
    <div className="space-y-4">
      <p className="text-sm text-gray-300">
        Share this authentication key to receive a file.
      </p>
      <div className="flex">
        <input
          type="text"
          readOnly
          value={wush?.auth_info().auth_key || ""}
          className="flex-grow p-3 border border-gray-600 rounded-l bg-gray-700 text-gray-200"
        />
        <button
          onClick={handleCopyAuthKey}
          className="p-3 bg-blue-600 rounded-r hover:bg-blue-700 transition-colors"
        >
          <Copy className="h-5 w-5" />
        </button>
        {wush?.auth_info().derp_name}
      </div>
    </div>
  );
};

export default Receive;
