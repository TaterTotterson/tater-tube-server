import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { execSync } from "child_process";
import { defineConfig } from "vite";
import { VitePWA } from "vite-plugin-pwa";

function getGitCommit(): string {
	if (process.env.GIT_COMMIT) return process.env.GIT_COMMIT;
	try {
		return execSync("git rev-parse --short HEAD").toString().trim();
	} catch {
		return "unknown";
	}
}

function getAppVersion(): string {
	if (process.env.APP_VERSION) return process.env.APP_VERSION;
	try {
		return execSync("git describe --tags --always --dirty").toString().trim();
	} catch {
		return process.env.npm_package_version || "0.0.0";
	}
}

// https://vite.dev/config/
export default defineConfig({
	plugins: [
		react(),
		tailwindcss(),
		VitePWA({
			registerType: "prompt",
			includeAssets: [
				"favicon.ico",
				"favicon-16x16.png",
				"favicon-32x32.png",
				"logo.png",
				"apple-touch-icon-180x180.png",
				"tater-tube-server-icon.png",
				"unraid-icon.png",
			],
			manifest: {
				name: "Tater Tube Server",
				short_name: "Tater Server",
				description: "A Tater-themed Usenet streaming backend.",
				theme_color: "#090909",
				background_color: "#090909",
				display: "standalone",
				start_url: "/",
				icons: [
					{
						src: "pwa-64x64.png",
						sizes: "64x64",
						type: "image/png",
					},
					{
						src: "pwa-192x192.png",
						sizes: "192x192",
						type: "image/png",
					},
					{
						src: "pwa-512x512.png",
						sizes: "512x512",
						type: "image/png",
					},
					{
						src: "maskable-icon-512x512.png",
						sizes: "512x512",
						type: "image/png",
						purpose: "maskable",
					},
				],
			},
			workbox: {
				navigateFallback: null,
				// Exclude html: index.html must NOT be precached so every navigation
				// reaches the network (and Authelia can check the session).
				globPatterns: ["**/*.{js,css,ico,png,svg,woff2}"],
				runtimeCaching: [
					// redirect: "manual" prevents the SW from following Authelia's 302
					// cross-origin to login.kipsi.top. The opaque redirect is passed back
					// to the browser which follows it as a normal navigation to the login page.
					// When the session is valid, the 200 HTML response is cached as usual.
					{
						urlPattern: ({ request }) => request.mode === "navigate",
						handler: "NetworkFirst",
						options: {
							cacheName: "navigation-cache",
							networkTimeoutSeconds: 10,
							fetchOptions: {
								redirect: "manual",
							},
							cacheableResponse: {
								statuses: [200],
							},
						},
					},
					// redirect: "manual" prevents the SW from following auth-proxy 302s
					// cross-origin (which would CORS-fail). The opaque redirect is passed
					// to the page; client.ts detects it and reloads through Authelia.
					{
						urlPattern: /^\/api\/.*/i,
						handler: "NetworkFirst",
						options: {
							cacheName: "api-cache",
							fetchOptions: {
								redirect: "manual",
							},
							expiration: {
								maxEntries: 50,
								maxAgeSeconds: 60 * 5,
							},
							networkTimeoutSeconds: 5,
						},
					},
				],
			},
		}),
	],
	define: {
		__APP_VERSION__: JSON.stringify(getAppVersion()),
		__GIT_COMMIT__: JSON.stringify(getGitCommit()),
		__GITHUB_URL__: JSON.stringify("https://github.com/TaterTotterson/tater-tube-server"),
	},
	server: {
		port: 5173,
		strictPort: true,
		proxy: {
			"/api": {
				target: "http://localhost:8080",
				changeOrigin: true,
				ws: true,
			},
			"/sabnzbd": {
				target: "http://localhost:8080",
				changeOrigin: true,
			},
		},
	},
});
