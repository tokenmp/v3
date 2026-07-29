/**
 * Reusable pure functions for request-log metric calculations & formatting.
 * Used by admin list/detail and user panel request pages.
 */

// ---------------------------------------------------------------------------
// Tokens/s calculation
// ---------------------------------------------------------------------------

/**
 * Compute output tokens per second.
 * - For streaming: generation time = durationMs - ttftMs
 * - For non-streaming: generation time = durationMs
 * - Returns null when output/duration are absent or streaming generation time
 *   is non-positive (clock rounding can make TTFT equal/slightly exceed total)
 */
export function calcTokensPerSecond(opts: {
  outputTokens: number | null | undefined;
  durationMs: number | null | undefined;
  ttftMs?: number | null | undefined;
  stream?: boolean | null;
}): number | null {
  const out = opts.outputTokens;
  const dur = opts.durationMs;
  if (out == null || out <= 0 || dur == null || dur <= 0) return null;

  const isStream = opts.stream === true;
  const ttft = opts.ttftMs ?? 0;
  const genMs = isStream && ttft > 0 ? dur - ttft : dur;
  if (genMs <= 0) return null;
  return (out / genMs) * 1000;
}

// ---------------------------------------------------------------------------
// Formatters
// ---------------------------------------------------------------------------

/** Format a millisecond duration for display: <1s → "Xms", ≥1s → "X.XXs" */
export function formatDuration(ms: number | null | undefined): string {
  if (ms == null) return '—';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

/** Format token count with locale grouping; null/undefined → "—" */
export function formatTokens(n: number | null | undefined): string {
  if (n == null) return '—';
  return n.toLocaleString();
}

/** Format tokens/s: null → "—", <10 → 1 decimal, ≥10 → rounded */
export function formatTokensPerSecond(toks: number | null | undefined): string {
  if (toks == null) return '—';
  if (toks < 10) return `${toks.toFixed(1)} tok/s`;
  return `${Math.round(toks)} tok/s`;
}

/** Short protocol label for table cells */
export function protocolLabel(p: string | null | undefined): string {
  switch (p) {
    case 'openai_chat': return 'Chat';
    case 'anthropic_messages': return 'Messages';
    case 'openai_responses': return 'Responses';
    case 'openai_images': return 'Images';
    default: return p ?? '—';
  }
}

/** Full protocol label for detail views */
export function protocolLabelFull(p: string | null | undefined): string {
  switch (p) {
    case 'openai_chat': return 'OpenAI Chat';
    case 'anthropic_messages': return 'Anthropic Messages';
    case 'openai_responses': return 'OpenAI Responses';
    case 'openai_images': return 'OpenAI Images';
    default: return p ?? '—';
  }
}

/** Stream badge text */
export function streamLabel(s: boolean | null | undefined): string {
  if (s == null) return '—';
  return s ? '流式' : '非流式';
}

/** Truncate UA for table display; full value in title attribute */
export function truncateUA(ua: string | null | undefined, max = 28): string {
  if (!ua) return '—';
  if (ua.length <= max) return ua;
  return ua.slice(0, max) + '…';
}

/** Thinking effort display */
export function thinkingLabel(effort: string | null | undefined, mode?: string | null): string {
  if (!effort && !mode) return '—';
  const parts: string[] = [];
  if (mode) parts.push(mode);
  if (effort) parts.push(effort);
  return parts.join(' · ');
}
