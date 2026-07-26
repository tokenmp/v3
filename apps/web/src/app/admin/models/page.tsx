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
  FormSection,
  NumberField,
  SwitchField,
  TextField,
} from '@/components/ui/field';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { cn } from '@/lib/utils';
import type { AdminModelConfig } from '@/types/admin';

type CapabilityKey = 'text' | 'tools' | 'vision' | 'thinking' | 'image';

const CAPABILITY_META: Record<
  CapabilityKey,
  { label: string; tone: string }
> = {
  text: { label: '文本', tone: 'bg-blue-50 text-blue-600' },
  tools: { label: '工具', tone: 'bg-violet-50 text-violet-600' },
  vision: { label: '视觉', tone: 'bg-amber-50 text-amber-600' },
  thinking: { label: '思考', tone: 'bg-emerald-50 text-emerald-600' },
  image: { label: '图像', tone: 'bg-pink-50 text-pink-600' },
};

const CAPABILITY_ORDER: CapabilityKey[] = [
  'text',
  'tools',
  'vision',
  'thinking',
  'image',
];

const FILTER_OPTIONS: { value: CapabilityKey | undefined; label: string }[] = [
  { value: undefined, label: '全部' },
  { value: 'text', label: '文本' },
  { value: 'tools', label: '工具' },
  { value: 'vision', label: '视觉' },
  { value: 'thinking', label: '思考' },
  { value: 'image', label: '图像' },
];

const QUERY_KEY = ['admin', 'model-configs'] as const;

function capabilityTone(cap: string): string {
  return (
    CAPABILITY_META[cap as CapabilityKey]?.tone ?? 'bg-muted text-muted-foreground'
  );
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
      toast.success('已删除');
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
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-lg font-semibold">模型管理</h1>
      </div>

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
        <button
          type="button"
          onClick={() => setCreating(true)}
          className="inline-flex h-[var(--control-height-sm)] items-center gap-1.5 rounded-sm bg-primary px-3 text-xs font-medium text-primary-foreground hover:opacity-90"
        >
          <Plus className="size-3.5" />
          新建模型
        </button>
      </div>

      {/* Table */}
      <div className="overflow-x-auto rounded-md border border-border">
        <Table>
          <TableHeader>
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
                  <TableCell className="font-mono text-xs">{m.id}</TableCell>
                  <TableCell className="font-medium">{m.displayName}</TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {m.capabilities.map((cap) => (
                        <span
                          key={cap}
                          className={cn(
                            'rounded-full px-1.5 py-0.5 text-[10px] font-medium',
                            capabilityTone(cap),
                          )}
                        >
                          {capabilityLabel(cap)}
                        </span>
                      ))}
                    </div>
                  </TableCell>
                  <TableCell>
                    {m.thinkingSupported ? (
                      <span className="rounded-full bg-emerald-50 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600">
                        支持
                      </span>
                    ) : (
                      <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                        不支持
                      </span>
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
        />
      ) : null}
    </div>
  );
}

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
            className={cn(
              'rounded-full px-2 py-0.5 text-[10px] font-medium transition-colors',
              on
                ? CAPABILITY_META[k].tone
                : 'bg-muted text-muted-foreground hover:bg-accent hover:text-foreground',
            )}
          >
            {CAPABILITY_META[k].label}
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

function ModelFormModal({
  item,
  onClose,
  onSaved,
}: {
  item?: AdminModelConfig;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = Boolean(item);
  const [id, setId] = useState(item?.id ?? '');
  const [displayName, setDisplayName] = useState(item?.displayName ?? '');
  const [capabilities, setCapabilities] = useState<string[]>(
    item?.capabilities ?? ['text'],
  );
  const [thinkingSupported, setThinkingSupported] = useState(
    item?.thinkingSupported ?? false,
  );
  const [contextWindow, setContextWindow] = useState<string>(
    item?.contextWindow != null ? String(item.contextWindow) : '',
  );
  const [maxOutputTokens, setMaxOutputTokens] = useState<string>(
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
      toast.success('模型已创建');
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
      toast.success('已保存');
      onSaved();
    },
    onError: (e: unknown) => {
      toast.error(e instanceof Error ? e.message : '保存失败');
    },
  });

  const submitting = createMutation.isPending || updateMutation.isPending;
  const canSubmit = isEdit ? displayName.trim().length > 0 : id.trim().length > 0 && displayName.trim().length > 0;

  /** Parse empty string → null, non-empty → number */
  const parseNullableInt = (v: string): number | null => {
    const trimmed = v.trim();
    if (trimmed === '') return null;
    const n = Number(trimmed);
    return Number.isNaN(n) ? null : n;
  };

  const submit = () => {
    if (!canSubmit) return;
    const cw = parseNullableInt(contextWindow);
    const mot = parseNullableInt(maxOutputTokens);
    if (isEdit) {
      updateMutation.mutate({
        displayName: displayName.trim(),
        capabilities,
        thinkingSupported,
        contextWindow: cw,
        maxOutputTokens: mot,
      });
    } else {
      createMutation.mutate({
        id: id.trim(),
        displayName: displayName.trim(),
        capabilities,
        thinkingSupported,
        contextWindow: cw,
        maxOutputTokens: mot,
      });
    }
  };

  return (
    <Modal
      open
      title={isEdit ? `编辑 ${item!.id}` : '新建模型'}
      onClose={onClose}
      maxWidth="lg"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={submit}
          submitLabel={isEdit ? '保存' : '创建'}
          submitting={submitting}
          disabled={!canSubmit}
        />
      }
    >
      <div className="space-y-5">
        <FormSection title="基本信息" cols={2}>
          <Field label="模型 ID" required>
            <TextField
              value={id}
              onChange={setId}
              placeholder="gpt-4o-mini"
              disabled={isEdit}
              className="font-mono"
            />
          </Field>
          <Field label="显示名" required>
            <TextField
              value={displayName}
              onChange={setDisplayName}
              placeholder="GPT-4o mini"
            />
          </Field>
        </FormSection>

        <FormSection title="能力标签" cols={1} description="点击切换模型支持的能力">
          <CapabilityEditor value={capabilities} onChange={setCapabilities} />
        </FormSection>

        <FormSection title="思考能力" cols={1}>
          <Field label="Thinking 支持">
            <SwitchField
              checked={thinkingSupported}
              onChange={setThinkingSupported}
            />
          </Field>
        </FormSection>

        <FormSection title="容量限制" cols={2} description="留空表示使用默认值">
          <Field label="上下文窗口（token）" hint="留空使用默认">
            <NumberField
              value={contextWindow}
              onChange={setContextWindow}
              placeholder="留空使用默认"
              min={1}
            />
          </Field>
          <Field label="最大输出 Token" hint="留空使用默认">
            <NumberField
              value={maxOutputTokens}
              onChange={setMaxOutputTokens}
              placeholder="留空使用默认"
              min={1}
            />
          </Field>
        </FormSection>
      </div>
    </Modal>
  );
}
