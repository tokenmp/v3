'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { RefreshCw } from 'lucide-react';
import { toast } from 'sonner';
import { adminConfigApi } from '@/lib/api/admin';
import { Button } from '@/components/ui/button';

/**
 * CompileButton publishes a new config snapshot via the Config Service
 * (POST /api/v1/admin/compile) and invalidates the react-query cache on
 * success.
 *
 * Why it exists: model/route/provider/credential edits are written to the
 * Config DB but do NOT reach the Executor until a new snapshot is compiled
 * and published. The Executor polls the latest snapshot every ~10s, so this
 * is the explicit "make my changes live" action. Without it, /v1/models and
 * the routing table stay stale and there is no error surfaced.
 */
export function CompileButton({ size = 'default' }: { size?: 'default' | 'sm' }) {
  const qc = useQueryClient();
  const compile = useMutation({
    mutationFn: () => adminConfigApi.compile(),
    onSuccess: () => {
      toast.success('已重新编译并发布 snapshot，约 10 秒后 executor 热加载生效');
      void qc.invalidateQueries();
    },
    onError: () => toast.error('编译失败，请检查配置'),
  });
  return (
    <Button variant="default" size={size} onClick={() => compile.mutate()} disabled={compile.isPending}>
      <RefreshCw className={`h-4 w-4 ${size === 'sm' ? '' : 'mr-1'} ${compile.isPending ? 'animate-spin' : ''}`} />
      {compile.isPending ? '编译中…' : '编译并发布'}
    </Button>
  );
}
