'use client';

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ThemeProvider } from '@/components/theme-provider';
import { useEffect, useState, type ReactNode } from 'react';
import { useAuthStore } from '@/lib/auth';
import { authApi } from '@/lib/api/auth';
import { refreshTokens } from '@/lib/api/core';

function AuthBootstrap() {
  useEffect(() => {
    let active = true;
    // Remove credentials persisted by versions predating the HttpOnly session BFF.
    window.localStorage.removeItem('tokenmp-auth');

    void (async () => {
      const refreshed = await refreshTokens();
      if (refreshed) {
        try {
          const user = await authApi.me();
          if (active) useAuthStore.getState().updateUser(user);
        } catch {
          if (active) useAuthStore.getState().logout();
        }
      }
      if (active) useAuthStore.getState().setHydrated();
    })();

    return () => {
      active = false;
    };
  }, []);

  return null;
}

export function Providers({ children }: { children: ReactNode }) {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 30_000,
            refetchOnWindowFocus: false,
            retry: 1,
          },
        },
      }),
  );

  return (
    <QueryClientProvider client={client}>
      <AuthBootstrap />
      <ThemeProvider>{children}</ThemeProvider>
    </QueryClientProvider>
  );
}
