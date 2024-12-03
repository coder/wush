import type { ReactElement, ReactNode } from "react";
import type { NextPage } from "next";
import type { AppProps } from "next/app";
import { WasmProvider } from "@/context/wush";
import Layout from "@/components/layout";
import "tailwindcss/tailwind.css";

export type NextPageWithLayout<P = Record<string, unknown>, IP = P> = NextPage<
  P,
  IP
> & {
  getLayout?: (page: ReactElement) => ReactNode;
};

type AppPropsWithLayout = AppProps & {
  Component: NextPageWithLayout;
};

export default function MyApp({ Component, pageProps }: AppPropsWithLayout) {
  // Use the layout defined at the page level, if available
  const getLayout =
    Component.getLayout ??
    (() => (
      <WasmProvider>
        <Layout>
          <Component {...pageProps} />
        </Layout>
      </WasmProvider>
    ));

  return getLayout(
    <WasmProvider>
      <Component {...pageProps} />
    </WasmProvider>
  );
}
