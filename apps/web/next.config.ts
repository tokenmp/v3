import type { NextConfig } from 'next';
import { resolve } from 'node:path';

const connectSrc = [
  "'self'",
  ...[process.env.NEXT_PUBLIC_API_BASE, process.env.NEXT_PUBLIC_BIZ_API_BASE, process.env.NEXT_PUBLIC_NOTICE_API_BASE]
    .flatMap((value) => {
      if (!value) return [];
      try {
        const url = new URL(value);
        return [url.origin, `${url.protocol === 'https:' ? 'wss:' : 'ws:'}//${url.host}`];
      } catch {
        return [];
      }
    }),
].join(' ');

const fallbackCsp = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self'",
  // Required by existing React style attributes; scripts remain nonce-only at runtime.
  "style-src-attr 'unsafe-inline'",
  "img-src 'self' blob: data:",
  "font-src 'self' data:",
  `connect-src ${connectSrc}`,
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
  "frame-ancestors 'none'",
].join('; ');

const config: NextConfig = {
  // The Playwright local-smoke server needs a distinct dev lock so it never
  // attaches to a developer's regular `.next` process.
  ...(process.env.E2E_NEXT_DIST_DIR ? { distDir: process.env.E2E_NEXT_DIST_DIR } : {}),
  allowedDevOrigins: ['127.0.0.1'],
  reactStrictMode: true,
  transpilePackages: ['@tokenmp/ui-tokens'],
  // Docker runs a repo-root build context, so tracing must retain the
  // workspace-relative apps/web server path copied by the standalone runner.
  output: 'standalone',
  outputFileTracingRoot: resolve(__dirname, '../..'),
  async headers() {
    return [
      {
        source: '/:path*',
        headers: [
          { key: 'Content-Security-Policy', value: fallbackCsp },
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
        ],
      },
    ];
  },
  turbopack: {
    root: resolve(__dirname, '../..'),
  },
};

export default config;
