import { Link } from "@remix-run/react";
import type React from "react";
import { useState } from "react";

export default function Component() {
  const [peerAuth, setPeerAuth] = useState("");
  const handleConnect = (e: React.FormEvent) => {
    e.preventDefault();
  };

  return (
    <div className="flex items-center justify-center h-full">
      <form onSubmit={handleConnect} className="w-full max-w-md">
        <div className="flex flex-col items-center">
          <h1 className="text-4xl mb-8 text-[#ff79c6] italic">$ wush</h1>
          <input
            type="text"
            value={peerAuth}
            onChange={(e) => setPeerAuth(e.target.value)}
            placeholder="Enter auth key"
            className="w-full px-4 py-2 rounded bg-[#44475a] text-[#f8f8f2] placeholder-[#6272a4] focus:outline-none focus:ring-2 focus:ring-[#bd93f9]"
          />

          <Link to={`/connect#${peerAuth}`}>
            <button
              type="submit"
              className="mt-4 px-6 py-2 border-2 border-green text-green rounded hover:bg-green hover:text-bg focus:bg-green focus:text-bg focus:ring-2 focus:ring-green transition duration-150 ease-in-out shadow-md hover:shadow-lg active:shadow-sm inline-flex items-center"
            >
              Connect
            </button>
          </Link>
        </div>
      </form>
    </div>
  );
}
