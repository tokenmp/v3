import type { Metadata } from 'next';
import { headers } from 'next/headers';
import { Geist, Geist_Mono } from 'next/font/google';
import { Providers } from '@/components/providers';
import { Toaster } from '@/components/ui/sonner';
import './globals.css';

const geistSans = Geist({
  variable: '--font-geist-sans',
  subsets: ['latin'],
});

const geistMono = Geist_Mono({
  variable: '--font-geist-mono',
  subsets: ['latin'],
});

export const metadata: Metadata = {
  title: { template: '%s | TokenMP', default: 'TokenMP' },
  description: 'AI 模型 API 网关',
};

export default async function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  // Reading the per-request nonce opts pages out of static HTML so Next can
  // propagate the nonce from proxy.ts to all framework-generated scripts.
  await headers();

  return (
    <html
      lang="zh-CN"
      className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}
      suppressHydrationWarning
    >
      <body className="min-h-full flex flex-col">
        <Providers>{children}</Providers>
        <Toaster />
      </body>
    </html>
  );
}
