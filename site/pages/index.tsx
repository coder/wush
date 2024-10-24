import Receive, { IncomingFile } from "components/Receive";
import Send from "components/Send";
import { createWush } from "context/wush";
import { Code, Copy, FileUp, Star } from "lucide-react";
import { useEffect, useState } from "react";

type TabType = "send" | "receive" | "access";

export default function Component() {
  const [authKey, setAuthKey] = useState("");
  const [activeTab, setActiveTab] = useState<TabType>("send");
  const [wush, setWush] = useState<Wush>();
  const [incomingFiles, setIncomingFiles] = useState<IncomingFile[]>([]);

  useEffect(() => {
    const wushPromise = createWush({
      onIncomingFile: async (peer, filename, sizeBytes) => {
        return new Promise<boolean>((resolve) => {
          const newFile: IncomingFile = {
            peer,
            filename,
            sizeBytes,
            downloading: false,
            bytesDownloaded: 0,
            download: async () => {
              // Update the file's downloading state
              setIncomingFiles(files => 
                files.map(f => 
                  f === newFile ? { ...f, downloading: true } : f
                )
              );
              resolve(true);
            },
            cancel: async () => {
              resolve(false);
              setIncomingFiles(files => files.filter(f => f !== newFile));
            }
          };
          
          setIncomingFiles(files => [...files, newFile]);
        });
      },
      onNewPeer: (peer) => {},
      downloadFile: async (peer, filename, sizeBytes, stream) => {
        console.log("downloading file", peer, filename, sizeBytes);
        
      },
    });

    wushPromise.then(setWush).catch(console.error);
    return () => {
      wushPromise.then((wush) => wush.stop());
    };
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (activeTab === "send") {
      // Handle send file logic
    } else if (activeTab === "access") {
      // Handle open access logic
    }
  };

  console.log("auth key", wush, wush?.auth_info());

  const handleCopyAuthKey = () => {
    navigator.clipboard.writeText(wush?.auth_info().auth_key || "");
    // You might want to add a toast notification here
  };

  return (
    <div className="min-h-screen flex flex-col bg-black text-gray-200">
      <header className="p-6 flex justify-between items-center bg-gray-950 border-b border-gray-800">
        <div className="flex items-center space-x-4">
          <h3 className="text-2xl font-bold text-white">⧉ wush</h3>
          <span className="text-sm text-gray-400 hidden sm:inline">v1.0.0</span>
        </div>
        <a
          href="https://github.com/coder/wush"
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center text-gray-400 hover:text-gray-200 transition-colors"
        >
          <Star className="h-5 w-5 mr-1" />
          <span>{process.env.GITHUB_STARS}</span>
        </a>
      </header>

      <main className="flex-1 p-8 pt-40 bg-gradient-to-br from-gray-950 via-gray-900 to-black">
        <div className="w-full max-w-lg mx-auto space-y-8">
          <div className="text-center space-y-4">
            <h1 className="text-4xl font-bold text-white">
              Send, Receive, Access
            </h1>
            <p className="text-gray-400">
              WireGuard-powered peer-to-peer file transfer and remote access
            </p>
            <div className="flex justify-center items-center space-x-2 text-sm text-gray-500">
              <span>Infinite File Size</span>
              <span>•</span>
              <span>E2E Encrypted</span>
              <span>•</span>
              <span>Command Line ↔ Browser</span>
            </div>
          </div>

          <div className="bg-gray-800 rounded-lg shadow-xl overflow-hidden border border-gray-700">
            <div className="flex border-b border-gray-700 relative">
              {["send", "receive", "access"].map((tab) => (
                <button
                  key={tab}
                  className={`flex-1 py-3 transition-all duration-300 ease-in-out ${
                    activeTab === tab
                      ? "text-blue-400"
                      : "text-gray-400 hover:text-gray-200"
                  }`}
                  onClick={() => setActiveTab(tab as TabType)}
                >
                  {tab.charAt(0).toUpperCase() + tab.slice(1)}
                </button>
              ))}
              <div
                className="absolute bottom-0 left-0 h-0.5 bg-blue-400 transition-all duration-300 ease-in-out"
                style={{
                  width: "33.333%",
                  transform: `translateX(${
                    ["send", "receive", "access"].indexOf(activeTab) * 100
                  }%)`,
                }}
              />
            </div>

            <div className="p-6 space-y-4">
              {activeTab === "send" && <Send wush={wush} />}

              {(activeTab === "receive" || activeTab === "access") && (
                <Receive wush={wush} />
              )}
            </div>
          </div>
        </div>
      </main>

      <footer className="border-t border-gray-800 p-4 mt-auto bg-gray-950">
        <div className="text-center text-gray-500">Made by Coder</div>
      </footer>
    </div>
  );
}
