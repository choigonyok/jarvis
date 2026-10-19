import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Ships a self-contained server bundle so the Docker image needs no node_modules.
  output: "standalone",
  // A name for this build, written into it: app/api/version answers with it,
  // so a page left open can tell when a newer one has been deployed.
  env: { JARVIS_BUILD: Date.now().toString(36) },
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          // Images come from this origin only (photos and thumbnails through
          // /api, previews from blob:, data: for drawn ones). An <img> pointing
          // anywhere else - one slipped into a reply - is not fetched at all.
          { key: "Content-Security-Policy", value: "img-src 'self' data: blob:" },
        ],
      },
    ];
  },
};

export default nextConfig;
