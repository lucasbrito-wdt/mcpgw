import type { MetadataRoute } from "next";

import { meta } from "../lib/i18n/messages/meta";

export const dynamic = "force-static";

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "mcpgw",
    short_name: "mcpgw",
    description: meta.en.description,
    background_color: "#07061A",
    theme_color: "#07061A",
    display: "standalone",
    icons: [
      { src: "/icon-192.png", sizes: "192x192", type: "image/png" },
      { src: "/icon-512.png", sizes: "512x512", type: "image/png" },
      { src: "/icon-maskable-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
    ],
  };
}
