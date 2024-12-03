import { useTheme } from "next-themes";
import { Toaster as Sonner } from "sonner";

type ToasterProps = React.ComponentProps<typeof Sonner>;

const Toaster = ({ ...props }: ToasterProps) => {
  const { theme = "system" } = useTheme();

  return (
    <Sonner
      theme={theme as ToasterProps["theme"]}
      className="toaster group"
      visibleToasts={9999999999}
      toastOptions={{
        classNames: {
          toast:
            "group toast group-[.toaster]:bg-gray-800 group-[.toaster]:text-gray-200 group-[.toaster]:border-gray-700 group-[.toaster]:shadow-xl",
          description: "group-[.toast]:text-gray-400",
          actionButton:
            "group-[.toast]:bg-blue-600 group-[.toast]:text-white group-[.toast]:hover:bg-blue-700",
          cancelButton:
            "group-[.toast]:bg-gray-700 group-[.toast]:text-gray-200 group-[.toast]:hover:bg-gray-600",
        },
      }}
      {...props}
    />
  );
};

export { Toaster };
