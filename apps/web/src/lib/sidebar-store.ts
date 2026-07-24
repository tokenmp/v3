import { create } from 'zustand';
import { persist, createJSONStorage } from 'zustand/middleware';

interface SidebarState {
  collapsed: boolean;
  toggle: () => void;
  setCollapsed: (v: boolean) => void;
}

/** Shared sidebar collapse state (desktop only). Persisted per-browser. */
export const useSidebarStore = create<SidebarState>()(
  persist(
    (set) => ({
      collapsed: false,
      toggle: () => set((s) => ({ collapsed: !s.collapsed })),
      setCollapsed: (v) => set({ collapsed: v }),
    }),
    {
      name: 'tokenmp-sidebar',
      storage: createJSONStorage(() => localStorage),
    },
  ),
);
