import { vitePlugin as remix } from "@remix-run/dev";
import { defineConfig } from "vite";
import tsconfigPaths from "vite-tsconfig-paths";
import wasm from "vite-plugin-wasm";
import topLevelAwait from "vite-plugin-top-level-await";
import compression from "vite-plugin-compression2";

export default defineConfig({
	plugins: [
		remix({
			ssr: false,
			future: {
				v3_fetcherPersist: true,
				v3_relativeSplatPath: true,
				v3_throwAbortReason: true,
			},
		}),
		compression({
			include: /\.(wasm)$/,
		}),
		tsconfigPaths(),
		wasm(),
		topLevelAwait(),
	],
	build: {
		assetsInlineLimit: 0,
	},
	ssr: {
		noExternal: ["remix-utils"],
	},
});
