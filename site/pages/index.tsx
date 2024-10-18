import { useState, useContext } from "react"
import { WushContext } from "context/wush"
import { Copy, FileUp, Code, Star } from "lucide-react"

type TabType = "send" | "receive" | "access"

const GITHUB_STAR_COUNT = "1,000";

const CommandLineInstructions = ({ type }: { type: "send" | "receive" | "access" }) => (
  <div className="mt-4 p-4 bg-gray-700 rounded-md">
    <h4 className="text-sm font-semibold mb-2 flex items-center text-gray-200">
      <Code className="mr-2 h-4 w-4" />
      Command-line Instructions
    </h4>
    <pre className="text-xs overflow-x-auto text-gray-300">
      {type === "receive" ? (
        `# To send a file:
wush send <file> <auth-key>`
      ) : (
        `# To start a wush server:
wush server`
      )}
    </pre>
  </div>
)

export default function Component() {
  const [authKey, setAuthKey] = useState("")
  const [activeTab, setActiveTab] = useState<TabType>("send")
  const wush = useContext(WushContext)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (activeTab === "send") {
      // Handle send file logic
    } else if (activeTab === "access") {
      // Handle open access logic
    }
  }

  console.log("auth key", wush, wush?.auth_info())

  const handleCopyAuthKey = () => {
    navigator.clipboard.writeText(wush?.auth_info().auth_key || "")
    // You might want to add a toast notification here
  }

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
          <span>{GITHUB_STAR_COUNT}</span>
        </a>
      </header>

      <main className="flex-1 flex items-center justify-center p-8 bg-gradient-to-br from-gray-950 via-gray-900 to-black">
        <div className="w-full max-w-md space-y-8">
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
                      ? 'text-blue-400'
                      : 'text-gray-400 hover:text-gray-200'
                  }`}
                  onClick={() => setActiveTab(tab as TabType)}
                >
                  {tab.charAt(0).toUpperCase() + tab.slice(1)}
                </button>
              ))}
              <div 
                className="absolute bottom-0 left-0 h-0.5 bg-blue-400 transition-all duration-300 ease-in-out"
                style={{ 
                  width: '33.333%', 
                  transform: `translateX(${["send", "receive", "access"].indexOf(activeTab) * 100}%)`
                }}
              />
            </div>

            <div className="p-6 space-y-4">
              {activeTab === "send" && (
                <form onSubmit={handleSubmit} className="space-y-4">
                  <p className="text-sm text-gray-300">
                    Enter the authentication key to send a file.
                  </p>
                  <input
                    type="text"
                    value={authKey}
                    onChange={(e) => setAuthKey(e.target.value)}
                    placeholder="Enter auth key"
                    className="w-full p-3 border border-gray-600 rounded bg-gray-700 text-gray-200"
                  />
                  <button type="submit" className="w-full p-3 bg-blue-600 rounded flex items-center justify-center hover:bg-blue-700 transition-colors">
                    <FileUp className="mr-2 h-5 w-5" />
                    Send File
                  </button>
                  <CommandLineInstructions type="send" />
                </form>
              )}

              {(activeTab === "receive" || activeTab === "access") && (
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
                  </div>
                  <CommandLineInstructions type="receive" />
                </div>
              )}
            </div>
          </div>
        </div>
      </main>

      <footer className="border-t border-gray-800 p-4 mt-auto bg-gray-950">
        <div className="text-center text-gray-500">
          Made by Coder
        </div>
      </footer>
    </div>
  )
}
