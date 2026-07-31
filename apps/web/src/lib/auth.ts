import { create } from 'zustand';
import type { AccessTokenResponse, User } from '@/types';

interface AuthState {
  /** Short-lived access token. Deliberately kept in memory only. */
  accessToken: string | null;
  user: User | null;
  isHydrated: boolean;

  login: (tokens: AccessTokenResponse, user: User) => void;
  setTokens: (tokens: AccessTokenResponse) => void;
  updateUser: (user: User) => void;
  logout: () => void;
  setHydrated: () => void;
}

/**
 * Browser auth state is intentionally non-persistent. The refresh token is
 * held by the same-origin session BFF in an HttpOnly cookie, never by JS.
 */
export const useAuthStore = create<AuthState>()((set) => ({
  accessToken: null,
  user: null,
  isHydrated: false,

  login: (tokens, user) =>
    set({ accessToken: tokens.access_token, user, isHydrated: true }),

  setTokens: (tokens) => set({ accessToken: tokens.access_token }),

  updateUser: (user) => set({ user }),

  logout: () => set({ accessToken: null, user: null, isHydrated: true }),

  setHydrated: () => set({ isHydrated: true }),
}));
