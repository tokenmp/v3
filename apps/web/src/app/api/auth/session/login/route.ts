import { NextRequest, NextResponse } from 'next/server';
import { callAuth, sameOrigin, tokenResponse } from '@/lib/server/auth-session';

export async function POST(request: NextRequest): Promise<NextResponse> {
  if (!sameOrigin(request)) {
    return NextResponse.json({ message: 'Forbidden' }, { status: 403 });
  }

  const body = await request.json().catch(() => null);
  if (!body) {
    return NextResponse.json({ message: 'Invalid request body' }, { status: 400 });
  }

  try {
    return tokenResponse(await callAuth(request, 'login', body));
  } catch {
    return NextResponse.json({ message: 'Authentication service unavailable' }, { status: 502 });
  }
}
