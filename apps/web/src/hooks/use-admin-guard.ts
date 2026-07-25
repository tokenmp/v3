'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { useAuthStore } from '@/lib/auth';

/** Redirect to /login when unauthenticated, or /panel when not admin. */
export function useAdminGuard() {
  const router = useRouter();
  const accessToken = useAuthStore((s) => s.accessToken);
  const user = useAuthStore((s) => s.user);
  const isHydrated = useAuthStore((s) => s.isHydrated);
  const isAuthenticated = Boolean(accessToken && user);
  const isAdmin = user?.role === 'admin';

  useEffect(() => {
    if (!isHydrated) return;
    if (!isAuthenticated) {
      router.replace('/login?reason=session_expired');
      return;
    }
    if (!isAdmin) {
      router.replace('/panel');
    }
  }, [isHydrated, isAuthenticated, isAdmin, router]);

  return { isHydrated, isAuthenticated, isAdmin };
}
