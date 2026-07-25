'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { useAuthStore } from '@/lib/auth';

/** Redirect to /login when not authenticated after hydration. */
export function useAuthGuard() {
  const router = useRouter();
  const accessToken = useAuthStore((s) => s.accessToken);
  const user = useAuthStore((s) => s.user);
  const isHydrated = useAuthStore((s) => s.isHydrated);
  const isAuthenticated = Boolean(accessToken && user);

  useEffect(() => {
    if (!isHydrated) return;
    if (!isAuthenticated) {
      router.replace('/login?reason=session_expired');
    }
  }, [isHydrated, isAuthenticated, router]);

  return { isHydrated, isAuthenticated };
}
