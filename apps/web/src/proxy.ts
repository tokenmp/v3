import { NextRequest, NextResponse } from 'next/server';

function connectSources(): string[] {
  const sources = new Set(["'self'"]);
  for (const value of [
    process.env.NEXT_PUBLIC_API_BASE,
    process.env.NEXT_PUBLIC_BIZ_API_BASE,
    process.env.NEXT_PUBLIC_NOTICE_API_BASE,
  ]) {
    if (!value) continue;
    try {
      const url = new URL(value);
      sources.add(url.origin);
      sources.add(`${url.protocol === 'https:' ? 'wss:' : 'ws:'}//${url.host}`);
    } catch {
      // Relative URLs are already covered by 'self'.
    }
  }
  return [...sources];
}

export function proxy(request: NextRequest) {
  const nonce = Buffer.from(crypto.randomUUID()).toString('base64');
  const csp = [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'`,
    `style-src 'self' 'nonce-${nonce}'`,
    // React uses bounded inline style attributes for dynamic layout values.
    "style-src-attr 'unsafe-inline'",
    "img-src 'self' blob: data:",
    "font-src 'self' data:",
    `connect-src ${connectSources().join(' ')}`,
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
  ].join('; ');

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set('x-nonce', nonce);
  requestHeaders.set('Content-Security-Policy', csp);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set('Content-Security-Policy', csp);
  return response;
}

export const config = {
  matcher: [{ source: '/((?!_next/static|_next/image|favicon.ico).*)' }],
};
