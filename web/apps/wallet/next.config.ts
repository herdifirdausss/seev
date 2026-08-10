import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  transpilePackages: ['@seev/domain', '@seev/money', '@seev/ui', '@seev/api-client'],
  webpack: (config) => {
    config.resolve.extensionAlias = {
      '.js': ['.ts', '.tsx', '.js', '.jsx'],
      '.mjs': ['.mts', '.mjs'],
    };
    return config;
  },
  // Wallet only calls Auth and Gateway public surfaces (K5).
  // No backend service URLs belong in browser bundles.
  typedRoutes: true,
  headers: async () => [
    {
      source: '/(.*)',
      headers: [
        { key: 'X-Frame-Options', value: 'DENY' },
        { key: 'X-Content-Type-Options', value: 'nosniff' },
        { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
        {
          key: 'Permissions-Policy',
          value: 'camera=(), microphone=(), geolocation=(), payment=()',
        },
        {
          key: 'Content-Security-Policy',
          value: [
            "default-src 'self'",
            "script-src 'self' 'unsafe-inline' 'unsafe-eval'",
            "style-src 'self' 'unsafe-inline'",
            "img-src 'self' data:",
            "connect-src 'self'",
            "font-src 'self'",
            "frame-src 'none'",
            "object-src 'none'",
          ].join('; '),
        },
      ],
    },
  ],
};

export default nextConfig;
