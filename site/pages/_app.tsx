import { AppProps } from 'next/app';
import { WushContext } from '../context/wush';
import { useEffect, useState } from 'react';
import "tailwindcss/tailwind.css"

export default function MyApp({ Component, pageProps }: AppProps) {
  const [wushCtx, setWushCtx] = useState<Wush | null>(null);

  useEffect(() => {
    // WASM initialization logic here (similar to your current root.tsx)
  }, []);

  return (
    <WushContext.Provider value={wushCtx}>
      <Component {...pageProps} />
    </WushContext.Provider>
  );
}
