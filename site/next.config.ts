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
	};
};

export default createConfig();
