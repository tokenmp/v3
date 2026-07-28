'use client';

import { useEffect, useRef, useState } from 'react';
import { cn } from '@/lib/utils';

interface BottomSheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: React.ReactNode;
  /** Max height as CSS value (default: 80vh) */
  maxHeight?: string;
}

/**
 * Mobile bottom sheet that slides up from the bottom.
 * Uses CSS transitions for smooth animation.
 */
export function BottomSheet({
  open,
  onOpenChange,
  children,
  maxHeight = '80vh',
}: BottomSheetProps) {
  const [isVisible, setIsVisible] = useState(false);
  const [isAnimating, setIsAnimating] = useState(false);
  const sheetRef = useRef<HTMLDivElement>(null);
  const startYRef = useRef(0);
  const currentYRef = useRef(0);
  const isDraggingRef = useRef(false);

  useEffect(() => {
    if (open) {
      setIsVisible(true);
      // Trigger animation after mount
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          setIsAnimating(true);
        });
      });
      // Prevent body scroll
      document.body.style.overflow = 'hidden';
    } else {
      setIsAnimating(false);
      // Wait for animation to complete before hiding
      const timer = setTimeout(() => {
        setIsVisible(false);
        document.body.style.overflow = '';
      }, 300);
      return () => clearTimeout(timer);
    }
    return () => {
      document.body.style.overflow = '';
    };
  }, [open]);

  const handleBackdropClick = () => {
    onOpenChange(false);
  };

  const handleTouchStart = (e: React.TouchEvent) => {
    const touch = e.touches[0];
    if (touch) {
      startYRef.current = touch.clientY;
    }
    isDraggingRef.current = true;
  };

  const handleTouchMove = (e: React.TouchEvent) => {
    if (!isDraggingRef.current) return;
    const touch = e.touches[0];
    if (touch) {
      currentYRef.current = touch.clientY;
    }
    const deltaY = currentYRef.current - startYRef.current;
    
    // Only allow dragging down
    if (deltaY > 0 && sheetRef.current) {
      sheetRef.current.style.transform = `translateY(${deltaY}px)`;
    }
  };

  const handleTouchEnd = () => {
    if (!isDraggingRef.current || !sheetRef.current) return;
    isDraggingRef.current = false;
    
    const deltaY = currentYRef.current - startYRef.current;
    const threshold = 100; // pixels to trigger close
    
    if (deltaY > threshold) {
      onOpenChange(false);
    } else {
      // Reset position
      sheetRef.current.style.transform = '';
    }
  };

  if (!isVisible) return null;

  return (
    <div className="fixed inset-0 z-50 md:hidden">
      {/* Backdrop */}
      <div
        className={cn(
          'absolute inset-0 bg-black/50 transition-opacity duration-300',
          isAnimating ? 'opacity-100' : 'opacity-0'
        )}
        onClick={handleBackdropClick}
      />
      
      {/* Sheet */}
      <div
        ref={sheetRef}
        className={cn(
          'absolute bottom-0 left-0 right-0 bg-background rounded-t-2xl transition-transform duration-300 ease-out',
          isAnimating ? 'translate-y-0' : 'translate-y-full'
        )}
        style={{ maxHeight }}
        onTouchStart={handleTouchStart}
        onTouchMove={handleTouchMove}
        onTouchEnd={handleTouchEnd}
      >
        {/* Drag handle */}
        <div className="flex justify-center p-2">
          <div className="w-10 h-1 rounded-full bg-muted-foreground/30" />
        </div>
        
        {/* Content */}
        <div className="overflow-y-auto" style={{ maxHeight: `calc(${maxHeight} - 2rem)` }}>
          {children}
        </div>
      </div>
    </div>
  );
}
