import { X } from "lucide-react";
import { Link } from "@remix-run/react";
import "@xterm/xterm/css/xterm.css";
import WushTerminal from "~/components/Terminal.client";

export default function Component() {
  const authKey = window.location.hash.slice(1);

  return (
    <div className="h-full">
      <div className="flex flex-col h-full">
        <div className="flex-grow border-t border-l border-r border-bg">
          <div className="h-full [&_.xterm-viewport]:!scrollbar-hide">
            <WushTerminal authKey={authKey} />
          </div>
        </div>
        <div className="flex justify-between items-center px-2 h-6 bg-[#007acc] text-white text-xs">
          <div>Connected to: {authKey}</div>
          <Link to="/">
            <button
              type="button"
              className="p-0.5 text-white bg-red-500 hover:bg-red-600 rounded focus:outline-none focus:ring-1 focus:ring-red-600"
              aria-label="Disconnect"
            >
              Disconnect
            </button>
          </Link>
        </div>
      </div>
    </div>
  );
}
