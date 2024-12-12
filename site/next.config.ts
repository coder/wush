import type { NextConfig } from "next";

// Define an async function to create the config
const createConfig = async (): Promise<NextConfig> => {
	const githubStars = await fetch("https://api.github.com/repos/coder/wush")
		.then((r) => r.json())
		.then((j) => j.stargazers_count.toString());

	return {
		env: {
			GITHUB_STARS: githubStars,
		},
		webpack: (config, { dev }) => {
			// Add WASM support
			config.experiments = {
				...config.experiments,
				asyncWebAssembly: true,
			};

			// Add rule for wasm files with content hashing
			config.module.rules.push({
				test: /\.wasm$/,
				type: "asset/resource",
				generator: {
					filename: dev
						? "static/wasm/[name].wasm"
						: "static/wasm/[name].[hash][ext]",
				},
			});

			return config;
		},
		headers: async () => [
			{
				source: "/:all*.wasm",
				headers: [
					{
						key: "Cache-Control",
						value: "public, max-age=31536000, immutable",
					},
				],
			},
		],
	};
};

export default createConfig();
