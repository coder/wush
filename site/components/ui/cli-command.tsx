import { useState } from "react";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { Check, Copy } from "lucide-react";

interface CliCommandCardProps {
  command: string;
}

export function CliCommandCard({ command }: CliCommandCardProps) {
  const [isCopied, setIsCopied] = useState(false);

  const copyToClipboard = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setIsCopied(true);
      toast.success("Command copied to clipboard");
      setTimeout(() => setIsCopied(false), 2000);
    } catch (err) {
      toast.error("Failed to copy command");
    }
  };

  return (
    <Card className="w-full max-w-2xl">
      <CardContent className="p-2 flex items-center justify-between border-gray-600 bg-gray-700 rounded">
        <code className="text-sm bg-muted px-[0.3rem] py-[0.2rem] rounded font-mono flex-grow mr-2 overflow-x-auto">
          {command}
        </code>
        <Button
          variant="outline"
          size="icon"
          onClick={copyToClipboard}
          className="flex-shrink-0 relative overflow-hidden"
        >
          <span
            className={`absolute inset-0 flex items-center justify-center transition-transform duration-300 ${
              isCopied ? "translate-y-full" : "translate-y-0"
            }`}
          >
            <Copy className="h-4 w-4" />
          </span>
          <span
            className={`absolute inset-0 flex items-center justify-center transition-transform duration-300 ${
              isCopied ? "translate-y-0" : "-translate-y-full"
            }`}
          >
            <Check className="h-4 w-4" />
          </span>
        </Button>
      </CardContent>
    </Card>
  );
}
