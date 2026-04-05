/** @type {import('next').NextConfig} */
const backendOrigin = process.env.NEXT_PUBLIC_BACKEND_ORIGIN ?? "http://127.0.0.1:3000";
const isDevelopment = process.env.NODE_ENV === "development";

const nextConfig = {
  distDir: isDevelopment ? ".next-dev" : ".next",
  output: "export",
  trailingSlash: true,
  images: {
    unoptimized: true,
  },
  poweredByHeader: false,
  ...(isDevelopment
    ? {
        async rewrites() {
          return [
            {
              source: "/api/:path*",
              destination: `${backendOrigin}/api/:path*`,
            },
            {
              source: "/assets/:path*",
              destination: `${backendOrigin}/assets/:path*`,
            },
          ];
        },
      }
    : {}),
};

module.exports = nextConfig;
