import { NextRequest, NextResponse } from 'next/server';
import {
  REFRESH_COOKIE,
  callAuth,
  clearRefreshCookie,
  sameOrigin,
} from '@/lib/server/auth-session';

export async function POST(request: NextRequest): Promise<NextResponse> {
  if (!sameOrigin(request)) {
    return NextResponse.json({ message: 'Forbidden' }, { status: 403 });
  }

  const refreshToken = request.cookies.get(REFRESH_COOKIE)?.value;
  try {
    if (refreshToken) {
      await callAuth(request, 'logout', { refresh_token: refreshToken });
    }
  } catch {
    // Local logout still clears the browser session when Auth is unavailable.
  }

  const response = new NextResponse(null, {
    status: 204,
    headers: { 'Cache-Control': 'no-store' },
  });
  clearRefreshCookie(response);
  return response;
}
