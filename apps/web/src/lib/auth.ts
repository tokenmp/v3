import { create } from 'zustand';
import { persist, createJSONStorage } from 'zustand/middleware';
import type { TokenResponse, User } from '@/types';

interface AuthState {
  accessToken: string | null;
  refreshToken: string | null;
  user: User | null;
  isHydrated: boolean;

  login: (tokens: TokenResponse, user: User) => void;
  setTokens: (tokens: TokenResponse) => void;
  updateUser: (user: User) => void;
  logout: () => void;
  setHydrated: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      accessToken: null,
      refreshToken: null,
      user: null,
      isHydrated: false,

      login: (tokens, user) =>
        set({
          accessToken: tokens.access_token,
          refreshToken: tokens.refresh_token,
          user,
          isHydrated: true,
        }),

      setTokens: (tokens) =>
        set({ accessToken: tokens.access_token, refreshToken: tokens.refresh_token }),

      updateUser: (user) => set({ user }),

      logout: () =>
        set({ accessToken: null, refreshToken: null, user: null }),

      setHydrated: () => set({ isHydrated: true }),
    }),
    {
      name: 'tokenmp-auth',
      storage: createJSONStorage(() => localStorage),
      partialize: (s) => ({
        accessToken: s.accessToken,
        refreshToken: s.refreshToken,
        user: s.user,
      }),
      onRehydrateStorage: () => (state) => {
        state?.setHydrated();
      },
    },
  ),
);
