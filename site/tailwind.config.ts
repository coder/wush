import type { Config } from "tailwindcss";
import { createThemes } from "tw-colors";

export default {
	content: ["./app/**/{**,.client,.server}/**/*.{js,jsx,ts,tsx}"],
	theme: {
		extend: {
			fontFamily: {
				sans: [
					'"Inter"',
					"ui-sans-serif",
					"system-ui",
					"sans-serif",
					'"Apple Color Emoji"',
					'"Segoe UI Emoji"',
					'"Segoe UI Symbol"',
					'"Noto Color Emoji"',
				],
			},
		},
	},
	plugins: [
		require("tailwind-scrollbar-hide"),
		createThemes({
			gruvbox: {
				bg: "#282828",
				fg: "#ebdbb2",
				red: "#cc241d",
				green: "#98971a",
				yellow: "#d79921",
				blue: "#458588",
				purple: "#b16286",
				aqua: "#689d6a",
				gray: "#a89984",
				"orange-red": "#fb4934",
				"orange-yellow": "#fabd2f",
			},
			iterm: {
				bg: "#000000", // Black background
				fg: "#ffffff", // White foreground text
				red: "#c91b00", // Red
				green: "#00c200", // Green
				yellow: "#c7c400", // Yellow
				blue: "#0225c7", // Blue
				purple: "#ca30c7", // Magenta (closest to purple)
				aqua: "#00c5c7", // Cyan (closest to aqua)
				gray: "#686868", // Bright Black (used for gray)
				"orange-red": "#c91b00", // Using red again as iTerm2 doesn't have a default orange-red
				"orange-yellow": "#c7c400", // Using yellow again as iTerm2 doesn't have a default orange-yellow
			},
			dracula: {
				bg: "#282a36",
				fg: "#f8f8f2",
				selection: "#44475a",
				comment: "#6272a4",
				cyan: "#8be9fd",
				green: "#50fa7b",
				orange: "#ffb86c",
				pink: "#ff79c6",
				purple: "#bd93f9",
				red: "#ff5555",
				yellow: "#f1fa8c",
			},
		}),
	],
} satisfies Config;
