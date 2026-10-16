import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Ships a self-contained server bundle so the Docker image needs no node_modules.
  output: "standalone",
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
