import 'server-only';

import { NextRequest, NextResponse } from 'next/server';
import type { TokenResponse } from '@/types';

export const REFRESH_COOKIE = '__Host-tokenmp-refresh';
const AUTH_PREFIX = '/api/v1/auth';
const COOKIE_MAX_AGE_SECONDS = 30 * 24 * 60 * 60;

type PublicTokenResponse = Omit<TokenResponse, 'refresh_token'>;

type TokenEnvelope = {
  code: number;
  data: TokenResponse;
  message: string;
};

function authBase(request: NextRequest): URL {
  const configured = process.env.AUTH_API_BASE ?? process.env.NEXT_PUBLIC_API_BASE ?? '';
  return new URL(configured || '/', request.nextUrl.origin);
}

export function authURL(request: NextRequest, operation: 'login' | 'refresh' | 'logout'): URL {
  return new URL(`${authBase(request).pathname.replace(/\/$/, '')}${AUTH_PREFIX}/${operation}`, authBase(request));
}

export function sameOrigin(request: NextRequest): boolean {
  const origin = request.headers.get('origin');
  if (origin === null) return true;

  try {
    const candidate = new URL(origin);
    const host = request.headers.get('x-forwarded-host') ?? request.headers.get('host');
    const protocol = request.headers.get('x-forwarded-proto') ?? request.nextUrl.protocol.slice(0, -1);
    return candidate.host === host && candidate.protocol === `${protocol}:`;
  } catch {
    return false;
  }
}

export async function callAuth(
  request: NextRequest,
  operation: 'login' | 'refresh' | 'logout',
  body: unknown,
): Promise<Response> {
  return fetch(authURL(request, operation), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    cache: 'no-store',
    redirect: 'error',
  });
}

function tokenPayload(value: unknown): TokenResponse | null {
  if (!value || typeof value !== 'object') return null;
  const candidate = value as Partial<TokenResponse>;
  if (
    typeof candidate.access_token !== 'string' ||
    typeof candidate.refresh_token !== 'string' ||
    candidate.token_type !== 'Bearer' ||
    typeof candidate.expires_in !== 'number'
  ) {
    return null;
  }
  return candidate as TokenResponse;
}

export async function tokenResponse(upstream: Response): Promise<NextResponse> {
  const value = (await upstream.json().catch(() => null)) as TokenEnvelope | TokenResponse | null;
  const tokens = value && 'code' in value && value.code === 0
    ? tokenPayload(value.data)
    : tokenPayload(value);

  if (!upstream.ok) {
    return NextResponse.json(value ?? { message: 'Authentication request failed' }, {
      status: upstream.status,
      headers: { 'Cache-Control': 'no-store' },
    });
  }
  if (!tokens) {
    return NextResponse.json({ message: 'Invalid authentication response' }, {
      status: 502,
      headers: { 'Cache-Control': 'no-store' },
    });
  }

  const publicTokens: PublicTokenResponse = {
    access_token: tokens.access_token,
    token_type: tokens.token_type,
    expires_in: tokens.expires_in,
  };
  const response = NextResponse.json(publicTokens, {
    headers: { 'Cache-Control': 'no-store' },
  });
  response.cookies.set(REFRESH_COOKIE, tokens.refresh_token, {
    httpOnly: true,
    secure: true,
    sameSite: 'strict',
    path: '/',
    maxAge: COOKIE_MAX_AGE_SECONDS,
  });
  return response;
}

export function clearRefreshCookie(response: NextResponse): void {
  response.cookies.set(REFRESH_COOKIE, '', {
    httpOnly: true,
    secure: true,
    sameSite: 'strict',
    path: '/',
    maxAge: 0,
  });
}
