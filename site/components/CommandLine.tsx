import { Code } from "lucide-react";

const CommandLine: React.FC<{
    readonly text: string
}> = (props) => (
  <div className="mt-4 p-4 bg-gray-700 rounded-md">
    <h4 className="text-sm font-semibold mb-2 flex items-center text-gray-200">
      <Code className="mr-2 h-4 w-4" />
      Command-line Instructions
    </h4>
    <pre className="text-xs overflow-x-auto text-gray-300">
      {props.text}
    </pre>
  </div>
);

export default CommandLine;
