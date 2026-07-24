'use client';

import { Toaster as SonnerToaster } from 'sonner';

export function Toaster() {
  return (
    <SonnerToaster
      position="top-center"
      richColors
      closeButton
      toastOptions={{
        classNames: {
          toast: 'rounded-lg',
        },
      }}
    />
  );
}
