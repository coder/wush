import { useEffect } from "react";
import { useRouter } from "next/router";

export default function Component() {
  const router = useRouter();
  useEffect(() => {
    router.push("/send");
  }, [router]);
}
