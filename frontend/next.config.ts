import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  async rewrites() {
      return [
        {
          source: '/api/:path*',
          destination: 'http://localhost:1984/api/:path*',
        },
      ];
  },
};

export default nextConfig;
