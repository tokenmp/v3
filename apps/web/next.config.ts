import type { NextConfig } from 'next';
import { resolve } from 'node:path';

const config: NextConfig = {
  reactStrictMode: true,
  transpilePackages: ['@tokenmp/ui-tokens'],
  output: 'standalone',
  turbopack: {
    root: resolve(__dirname, '../..'),
  },
};

export default config;
