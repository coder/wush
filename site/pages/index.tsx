import { useEffect } from "react";
import { useRouter } from "next/router";

export default function Component() {
  const router = useRouter();
  useEffect(() => {
    const currentHash = window.location.hash.substring(1);
    if (currentHash) {
      router.push(`/send#${currentHash}`);
    } else {
      router.push("/send");
    }
  }, [router]);
}
