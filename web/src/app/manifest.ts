import type { MetadataRoute } from "next";

/**
 * What a phone needs to open this from the home screen as an app: full
 * screen, no browser bars, the app's own colour behind it while it starts.
 */
export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Jarvis",
    short_name: "Jarvis",
    start_url: "/",
    scope: "/",
    display: "standalone",
    background_color: "#0b0d12",
    theme_color: "#0b0d12",
    icons: [
      { src: "/icons/icon-192.v2.png", sizes: "192x192", type: "image/png" },
      { src: "/icons/icon-512.v2.png", sizes: "512x512", type: "image/png" },
      { src: "/icons/icon-512-maskable.v2.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
    ],
  };
}
