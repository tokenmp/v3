'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { adminConfigApi } from '@/lib/api/admin';
import { FilterChip } from '@/components/filter-chip';
import { Modal } from '@/components/ui/modal';
import {
  Field,
  FormActions,
  TextField,
} from '@/components/ui/field';
import { Badge } from '@/components/ui/badge';
import { PublishStatusHint } from '@/components/publish-status-hint';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Button } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { PageHeader } from '@/components/page-header';
import { cn } from '@/lib/utils';
import type { AdminModelConfig } from '@/types/admin';

type CapabilityKey = 'text' | 'tools' | 'vision' | 'thinking' | 'image';

const CAPABILITY_META: Record<
  CapabilityKey,
  { label: string; variant: 'info' | 'default' | 'warning' | 'success' | 'destructive' | 'secondary' | 'outline' }
> = {
  text: { label: '文本', variant: 'info' },
  tools: { label: '工具', variant: 'default' },
  vision: { label: '视觉', variant: 'warning' },
  thinking: { label: '思考', variant: 'success' },
  image: { label: '图像', variant: 'destructive' },
};

const CAPABILITY_ORDER: CapabilityKey[] = [
  'text',
  'tools',
  'vision',
  'thinking',
  'image',
];

const THINKING_EFFORTS = [
  { value: 'none', label: '无', description: '不使用思考' },
  { value: 'minimal', label: '最小', description: '最少的思考' },
  { value: 'low', label: '低', description: '轻度思考' },
  { value: 'medium', label: '中', description: '平衡思考' },
  { value: 'high', label: '高', description: '深度思考' },
  { value: 'xhigh', label: '极高', description: '非常深度的思考' },
  { value: 'max', label: '最大', description: '最大思考深度' },
] as const;

const FILTER_OPTIONS: { value: CapabilityKey | undefined; label: string }[] = [
  { value: undefined, label: '全部' },
  { value: 'text', label: '文本' },
  { value: 'tools', label: '工具' },
  { value: 'vision', label: '视觉' },
  { value: 'thinking', label: '思考' },
  { value: 'image', label: '图像' },
];

const QUERY_KEY = ['admin', 'model-configs'] as const;

function capabilityVariant(cap: string) {
  return CAPABILITY_META[cap as CapabilityKey]?.variant ?? 'secondary';
}

function capabilityLabel(cap: string): string {
  return CAPABILITY_META[cap as CapabilityKey]?.label ?? cap;
}

export default function AdminModelsPage() {
  const queryClient = useQueryClient();
  const { data: models = [], isLoading } = useQuery({
    queryKey: QUERY_KEY,
    queryFn: adminConfigApi.listModels,
  });

  const [search, setSearch] = useState('');
  const [capFilter, setCapFilter] = useState<CapabilityKey | undefined>(undefined);
  const [editing, setEditing] = useState<AdminModelConfig | null>(null);
  const [creating, setCreating] = useState(false);

  const filtered = useMemo(() => {
    const kw = search.trim().toLowerCase();
    return models.filter((m) => {
      const matchKw =
        !kw ||
        m.id.toLowerCase().includes(kw) ||
        m.displayName.toLowerCase().includes(kw);
      const matchCap = !capFilter || m.capabilities.includes(capFilter);
      return matchKw && matchCap;
    });
  }, [models, search, capFilter]);

  const deleteMutation = useMutation({
    mutationFn: (id: string) => adminConfigApi.deleteModel(id),
    onSuccess: () => {
      toast.success('已删除（需点编译并发布生效）');
      setEditing(null);
      void queryClient.invalidateQueries({ queryKey: QUERY_KEY });
    },
    onError: (e: unknown) => {
      toast.error(e instanceof Error ? e.message : '删除失败');
    },
  });

  const onDelete = (m: AdminModelConfig) => {
    if (!confirm(`确定删除模型「${m.displayName}」（${m.id}）？`)) return;
    deleteMutation.mutate(m.id);
  };

  return (
    <div className="space-y-4">
      <PageHeader title="模型配置" actions={<PublishStatusHint />} />

      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="text"
          placeholder="搜索 ID / 显示名"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-[var(--control-height-sm)] min-w-56 flex-1 rounded-sm border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
        <div className="flex flex-wrap gap-1.5">
          {FILTER_OPTIONS.map((opt) => (
            <FilterChip
              key={opt.label}
              label={opt.label}
              active={capFilter === opt.value}
              onClick={() => setCapFilter(opt.value)}
            />
          ))}
        </div>
        <Button
          type="button"
          size="sm"
          onClick={() => setCreating(true)}
        >
          <Plus className="size-3.5" />
          新建模型
        </Button>
      </div>

      {/* Table */}
      <div className="hidden md:block">
      <div className="overflow-hidden rounded-lg border border-border bg-card">
        <Table>
          <TableHeader className="bg-muted/30">
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>显示名</TableHead>
              <TableHead>能力</TableHead>
              <TableHead>Thinking</TableHead>
              <TableHead className="text-center">路由数</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              <TableRow>
                <TableCell colSpan={6} className="py-8 text-center text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="py-8 text-center text-muted-foreground">
                  暂无模型配置
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((m) => (
                <TableRow key={m.id}>
                  <TableCell className="font-mono">{m.id}</TableCell>
                  <TableCell className="font-medium">{m.displayName}</TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {m.capabilities.map((cap) => (
                        <Badge
                          key={cap}
                          variant={capabilityVariant(cap)}
                          className="rounded-full px-1.5 py-0.5 text-[10px]"
                        >
                          {capabilityLabel(cap)}
                        </Badge>
                      ))}
                    </div>
                  </TableCell>
                  <TableCell>
                    {m.thinkingSupported ? (
                      <Badge variant="success" className="rounded-full px-1.5 py-0.5 text-[10px]">支持</Badge>
                    ) : (
                      <Badge variant="secondary" className="rounded-full px-1.5 py-0.5 text-[10px]">不支持</Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-center tabular-nums">{m.routeCount}</TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <button
                        type="button"
                        onClick={() => setEditing(m)}
                        aria-label="编辑"
                        title="编辑"
                        className="rounded-sm p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                      >
                        <Pencil className="size-3.5" />
                      </button>
                      <button
                        type="button"
                        onClick={() => onDelete(m)}
                        aria-label="删除"
                        title="删除"
                        className="rounded-sm p-1.5 text-muted-foreground hover:bg-red-100 hover:text-red-700"
                      >
                        <Trash2 className="size-3.5" />
                      </button>
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>
      </div>

      {/* Mobile card list */}
      <div className="md:hidden space-y-3">
        {isLoading ? (
          <p className="py-8 text-center text-sm text-muted-foreground">加载中…</p>
        ) : filtered.length === 0 ? (
          <p className="py-8 text-center text-sm text-muted-foreground">暂无模型配置</p>
        ) : filtered.map((m) => (
          <button
            key={m.id}
            type="button"
            onClick={() => setEditing(m)}
            className="w-full text-left rounded-lg border bg-card p-3 space-y-2 active:bg-accent/50 transition-colors"
          >
            <div className="flex items-center justify-between gap-2">
              <div className="min-w-0">
                <p className="text-sm font-medium truncate">{m.displayName}</p>
                <p className="font-mono text-[10px] text-muted-foreground truncate">{m.id}</p>
              </div>
              <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground tabular-nums">{m.routeCount} 路由</span>
            </div>
            <div className="flex flex-wrap gap-1">
              {m.capabilities.map((cap) => (
                <Badge key={cap} variant={capabilityVariant(cap)} className="rounded-full px-1.5 py-0.5 text-[10px]">
                  {capabilityLabel(cap)}
                </Badge>
              ))}
            </div>
            <Badge variant={m.thinkingSupported ? 'success' : 'secondary'} className="rounded-full px-1.5 py-0.5 text-[10px]">
              {m.thinkingSupported ? '支持思考' : '不支持思考'}
            </Badge>
          </button>
        ))}
      </div>

      {creating ? (
        <ModelFormModal
          onClose={() => setCreating(false)}
          onSaved={() => {
            setCreating(false);
            void queryClient.invalidateQueries({ queryKey: QUERY_KEY });
          }}
        />
      ) : null}
      {editing ? (
        <ModelFormModal
          item={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void queryClient.invalidateQueries({ queryKey: QUERY_KEY });
          }}
          onDelete={() => onDelete(editing)}
        />
      ) : null}
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Capability Editor                                                          */
/* -------------------------------------------------------------------------- */

function CapabilityEditor({
  value,
  onChange,
}: {
  value: string[];
  onChange: (v: string[]) => void;
}) {
  const known = new Set<string>(CAPABILITY_ORDER);
  const extra = value.filter((v) => !known.has(v));
  return (
    <div className="flex flex-wrap gap-1.5">
      {CAPABILITY_ORDER.map((k) => {
        const on = value.includes(k);
        return (
          <button
            key={k}
            type="button"
            onClick={() =>
              onChange(on ? value.filter((v) => v !== k) : [...value, k])
            }
            className="p-0 leading-none"
          >
            <Badge
              variant={on ? CAPABILITY_META[k].variant : 'secondary'}
              className="rounded-full px-2 py-0.5 text-[10px] font-medium transition-colors hover:opacity-80"
            >
              {CAPABILITY_META[k].label}
            </Badge>
          </button>
        );
      })}
      {extra.map((k) => (
        <span
          key={k}
          className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground"
        >
          {k}
        </span>
      ))}
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Model Form Modal with tabs                                                 */
/* -------------------------------------------------------------------------- */

type TabKey = 'basic' | 'thinking' | 'capacity';

function ModelFormModal({
  item,
  onClose,
  onSaved,
  onDelete,
}: {
  item?: AdminModelConfig;
  onClose: () => void;
  onSaved: () => void;
  onDelete?: () => void;
}) {
  const isEdit = Boolean(item);
  const [tab, setTab] = useState<TabKey>('basic');

  // Basic info
  const [id, setId] = useState(item?.id ?? '');
  const [displayName, setDisplayName] = useState(item?.displayName ?? '');
  const [capabilities, setCapabilities] = useState<string[]>(
    item?.capabilities ?? ['text'],
  );

  // Thinking
  const [thinkingSupported, setThinkingSupported] = useState(
    item?.thinkingSupported ?? false,
  );
  const [maxEffort, setMaxEffort] = useState<string>(
    item?.thinkingMaxEffort ?? 'high',
  );
  const [defaultEffort, setDefaultEffort] = useState<string>(
    item?.thinkingDefaultEffort ?? 'medium',
  );

  // Capacity
  const [contextWindow, setContextWindow] = useState(
    item?.contextWindow != null ? String(item.contextWindow) : '',
  );
  const [maxOutputTokens, setMaxOutputTokens] = useState(
    item?.maxOutputTokens != null ? String(item.maxOutputTokens) : '',
  );

  const createMutation = useMutation({
    mutationFn: (input: {
      id: string;
      displayName: string;
      capabilities?: string[];
      thinkingSupported?: boolean;
      contextWindow?: number | null;
      maxOutputTokens?: number | null;
    }) => adminConfigApi.createModel(input),
    onSuccess: () => {
      toast.success('模型已创建（需点“编译并发布”生效）');
      onSaved();
    },
    onError: (e: unknown) => {
      toast.error(e instanceof Error ? e.message : '创建失败');
    },
  });

  const updateMutation = useMutation({
    mutationFn: (input: Partial<AdminModelConfig>) =>
      adminConfigApi.updateModel(item!.id, input),
    onSuccess: () => {
      toast.success('已保存（需点“编译并发布”生效）');
      onSaved();
    },
    onError: (e: unknown) => {
      toast.error(e instanceof Error ? e.message : '保存失败');
    },
  });

  const submitting = createMutation.isPending || updateMutation.isPending;
  const canSubmit = isEdit ? displayName.trim().length > 0 : id.trim().length > 0 && displayName.trim().length > 0;

  const parseNullableInt = (v: string): number | null => {
    const trimmed = v.trim();
    if (trimmed === '') return null;
    const n = Number(trimmed);
    return Number.isNaN(n) ? null : n;
  };

  const submit = () => {
    if (!canSubmit) return;
    const payload = {
      displayName: displayName.trim(),
      capabilities,
      thinkingSupported,
      thinkingDefaultEffort: thinkingSupported ? defaultEffort : null,
      thinkingMaxEffort: thinkingSupported ? maxEffort : null,
      contextWindow: parseNullableInt(contextWindow),
      maxOutputTokens: parseNullableInt(maxOutputTokens),
    };
    if (isEdit) {
      updateMutation.mutate(payload);
    } else {
      createMutation.mutate({ id: id.trim(), ...payload });
    }
  };

  const tabs: { key: TabKey; label: string }[] = [
    { key: 'basic', label: '基本信息' },
    { key: 'thinking', label: '思考深度' },
    { key: 'capacity', label: '容量限制' },
  ];

  return (
    <Modal
      open
      title={isEdit ? item!.displayName : '新建模型'}
      onClose={onClose}
      maxWidth="lg"
      footer={
        <div className="flex items-center justify-between w-full">
          {isEdit && onDelete ? (
            <button
              type="button"
              onClick={onDelete}
              className="text-sm text-destructive hover:underline"
            >
              删除模型
            </button>
          ) : (
            <div />
          )}
          <FormActions
            onCancel={onClose}
            onSubmit={submit}
            submitLabel={isEdit ? '保存' : '创建'}
            submitting={submitting}
            disabled={!canSubmit}
          />
        </div>
      }
    >
      {/* Tabs */}
      <div className="flex border-b mb-4">
        {tabs.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => setTab(t.key)}
            className={cn(
              'flex-1 py-2.5 text-sm font-medium border-b-2 transition-colors',
              tab === t.key
                ? 'border-primary text-primary'
                : 'border-transparent text-muted-foreground hover:text-foreground',
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {tab === 'basic' && (
        <div className="space-y-4">
          {isEdit && (
            <div className="rounded-lg bg-muted/50 p-3">
              <p className="font-mono text-xs text-muted-foreground">{item!.id}</p>
            </div>
          )}
          {!isEdit && (
            <Field label="模型 ID" required>
              <TextField
                value={id}
                onChange={setId}
                placeholder="gpt-4o-mini"
                className="font-mono"
              />
            </Field>
          )}
          <Field label="显示名" required>
            <TextField
              value={displayName}
              onChange={setDisplayName}
              placeholder="GPT-4o mini"
            />
          </Field>
          <Field label="能力标签" hint="点击切换">
            <CapabilityEditor value={capabilities} onChange={setCapabilities} />
          </Field>
        </div>
      )}

      {tab === 'thinking' && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <span className="text-sm">启用 Thinking</span>
            <Switch
              checked={thinkingSupported}
              onChange={setThinkingSupported}
              label="启用 Thinking"
            />
          </div>

          {thinkingSupported && (
            <div className="space-y-4">
              <div className="space-y-2">
                <p className="text-sm font-medium">支持的思考深度</p>
                <p className="text-xs text-muted-foreground">勾选的即为可用，点击切换</p>
                <div className="space-y-2">
                  {THINKING_EFFORTS.map((effort) => {
                    const effortIdx = THINKING_EFFORTS.findIndex((e) => e.value === effort.value);
                    const maxIdx = THINKING_EFFORTS.findIndex((e) => e.value === maxEffort);
                    const isSelected = effortIdx <= maxIdx;
                    return (
                      <label
                        key={effort.value}
                        className="flex items-center gap-3 rounded-lg border p-3 cursor-pointer hover:bg-accent/50 transition-colors"
                      >
                        <input
                          type="checkbox"
                          checked={isSelected}
                          onChange={() => setMaxEffort(effort.value)}
                          className="h-4 w-4 rounded border-input"
                        />
                        <div className="flex-1">
                          <span className="text-sm font-medium">{effort.label}</span>
                          <span className="text-xs text-muted-foreground ml-2">{effort.value}</span>
                        </div>
                      </label>
                    );
                  })}
                </div>
              </div>

              <div className="space-y-2">
                <p className="text-sm font-medium">默认思考深度</p>
                <select
                  className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm"
                  value={defaultEffort}
                  onChange={(e) => setDefaultEffort(e.target.value)}
                >
                  {THINKING_EFFORTS.filter((e) => {
                    const maxIdx = THINKING_EFFORTS.findIndex((x) => x.value === maxEffort);
                    return THINKING_EFFORTS.findIndex((x) => x.value === e.value) <= maxIdx;
                  }).map((effort) => (
                    <option key={effort.value} value={effort.value}>
                      {effort.label} ({effort.value})
                    </option>
                  ))}
                </select>
              </div>
            </div>
          )}

          {!thinkingSupported && (
            <p className="text-sm text-muted-foreground text-center py-8">
              该模型不支持 Thinking
            </p>
          )}
        </div>
      )}

      {tab === 'capacity' && (
        <div className="space-y-4">
          <Field label="上下文窗口（token）" hint="留空使用默认值">
            <input
              type="number"
              value={contextWindow}
              onChange={(e) => setContextWindow(e.target.value)}
              placeholder="留空使用默认"
              min={1}
              className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
            />
          </Field>
          <Field label="最大输出 Token" hint="留空使用默认值">
            <input
              type="number"
              value={maxOutputTokens}
              onChange={(e) => setMaxOutputTokens(e.target.value)}
              placeholder="留空使用默认"
              min={1}
              className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
            />
          </Field>
        </div>
      )}
    </Modal>
  );
}
