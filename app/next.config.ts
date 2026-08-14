import type { NextConfig } from "next"

const apiOrigin = process.env.AGENTBOX_API_URL || "http://127.0.0.1:8091"

const nextConfig: NextConfig = {
  allowedDevOrigins: ["127.0.0.1"],
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${apiOrigin}/api/:path*`,
      },
    ]
  },
}

export default nextConfig
