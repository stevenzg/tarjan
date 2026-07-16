import { createMDX } from "fumadocs-mdx/next";
import type { NextConfig } from "next";

// When deploying to GitHub Pages under a repo subpath, set PAGES_BASE_PATH
// (e.g. "/tarjan"). Locally it stays empty so dev/build just work.
const basePath = process.env.PAGES_BASE_PATH || "";

const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true,
  images: { unoptimized: true },
  basePath: basePath || undefined,
  assetPrefix: basePath || undefined,
};

const withMDX = createMDX();

export default withMDX(nextConfig);
