import { NextRequest, NextResponse } from 'next/server';
import {
  REFRESH_COOKIE,
  callAuth,
  clearRefreshCookie,
  sameOrigin,
  tokenResponse,
} from '@/lib/server/auth-session';

export async function POST(request: NextRequest): Promise<NextResponse> {
  if (!sameOrigin(request)) {
    return NextResponse.json({ message: 'Forbidden' }, { status: 403 });
  }

  const refreshToken = request.cookies.get(REFRESH_COOKIE)?.value;
  if (!refreshToken) {
    return NextResponse.json({ message: 'Session expired' }, { status: 401 });
  }

  try {
    const response = await tokenResponse(
      await callAuth(request, 'refresh', { refresh_token: refreshToken }),
    );
    if (!response.ok) clearRefreshCookie(response);
    return response;
  } catch {
    return NextResponse.json({ message: 'Authentication service unavailable' }, { status: 502 });
  }
}
