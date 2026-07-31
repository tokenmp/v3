'use client';

import { Badge } from '@/components/ui/badge';
import type { VariantProps } from 'class-variance-authority';

type BadgeVariant = NonNullable<VariantProps<typeof Badge>['variant']>;

/**
 * Map a request-log / entity status string to a Badge variant + label.
 * Covers all status values used across admin and panel request-logs,
 * credentials, providers, endpoints, API keys, and service health.
 */
function statusMapping(status: string): { variant: BadgeVariant; label: string } {
  switch (status) {
    // Request log statuses
    case 'success':
      return { variant: 'success', label: '成功' };
    case 'processing':
      return { variant: 'info', label: '处理中' };
    case 'cancelled':
    case 'client_cancelled':
      return { variant: 'warning', label: '已取消' };
    case 'error':
    case 'upstream_error':
    case 'timeout':
    case 'transport_error':
    case 'client_error':
      return { variant: 'destructive', label: '失败' };

    // Entity statuses (credentials, providers, endpoints, API keys)
    case 'active':
      return { variant: 'success', label: '启用' };
    case 'disabled':
      return { variant: 'secondary', label: '停用' };

    // Service health
    case 'up':
      return { variant: 'success', label: '运行中' };
    case 'down':
      return { variant: 'destructive', label: '不可用' };
    case 'checking':
      return { variant: 'secondary', label: '检查中…' };

    default:
      return { variant: 'secondary', label: status };
  }
}

/**
 * Reusable status badge that maps a status string to a semantic Badge variant.
 *
 * Usage:
 *   <StatusBadge status="success" />
 *   <StatusBadge status="active" />
 *   <StatusBadge status="processing" className="animate-pulse" />
 */
export function StatusBadge({
  status,
  className,
}: {
  status: string;
  className?: string;
}) {
  const { variant, label } = statusMapping(status);
  return (
    <Badge variant={variant} className={className}>
      {label}
    </Badge>
  );
}

/**
 * Map a request-log status to a Badge variant (for cases where the caller
 * renders its own label). Returns the variant string only.
 */
export function statusVariant(status: string): BadgeVariant {
  return statusMapping(status).variant;
}

/**
 * Map a request-log status to a human label (for cases where the caller
 * renders its own Badge). Returns the label string only.
 */
export function statusLabel(status: string): string {
  return statusMapping(status).label;
}
