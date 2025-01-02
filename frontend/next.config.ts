import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: 'standalone',
  eslint: {
    ignoreDuringBuilds: true,
  },
  typescript: {
    ignoreBuildErrors: true,
  },
  async rewrites() {
      return [
        {
          source: '/api/:path*',
          destination: 'http://127.0.0.1:1984/api/:path*',
        },
      ];
  },
};

export default nextConfig;
