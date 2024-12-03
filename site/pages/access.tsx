import { FileUp, TerminalIcon } from "lucide-react";
import { useWasm } from "@/context/wush";
import Link from "next/link";

export default function Component() {
  const wasm = useWasm();
  const isSubmitDisabled =
    !wasm.connectedPeer || wasm.connectedPeer.type !== "cli";
  const currentFragment = (() => {
    // Check if we're in the browser
    return typeof window !== "undefined"
      ? window.location.hash.substring(1)
      : "";
  })();

  console.log(wasm.connectedPeer);
  return (
    <div className="space-y-4">
      {wasm.connectedPeer?.type === "web" && (
        <p className="text-sm text-gray-300 pb-3">
          Terminals are only available when connected to a CLI. Use{" "}
          <code>wush serve</code> instead.
        </p>
      )}
      <Link href={`/terminal#${currentFragment}`}>
        <button
          type="submit"
          className={`w-full p-3 bg-blue-600 rounded flex items-center justify-center transition-colors ${
            isSubmitDisabled
              ? "opacity-50 cursor-not-allowed"
              : "hover:bg-blue-700"
          }`}
          disabled={isSubmitDisabled}
        >
          <TerminalIcon className="mr-2 h-5 w-5" />
          Open Terminal
        </button>
      </Link>
    </div>
  );
}
