/**
 * Pure helpers that turn usage-history rows into what the Usage page shows:
 * one account (or all Claude accounts combined), grouped by model, account and day.
 */

import type { UsageRow, UsageTotals } from '@/services/api/usageHistory';
import { maskAccountName } from '@/features/quota/routing/routingFormat';

export type ModelFamily = 'opus' | 'fable' | 'sonnet' | 'haiku' | 'other';

/** Fixed series order (bottom of the stack first). Color follows the family, never its rank. */
export const FAMILY_ORDER: readonly ModelFamily[] = ['opus', 'fable', 'sonnet', 'haiku', 'other'];

/** `all` means every Claude account combined. Otherwise `${provider}:${account}`. */
export type AccountScope = 'all' | string;

export const ALL_ACCOUNTS: AccountScope = 'all';

export const emptyTotals = (): UsageTotals => ({
  requests: 0,
  failed: 0,
  in: 0,
  out: 0,
  cache_read: 0,
  cache_write_5m: 0,
  cache_write_1h: 0,
  cost_usd: 0,
  unpriced: 0,
});

export function addTotals(target: UsageTotals, source: UsageTotals): UsageTotals {
  target.requests += source.requests;
  target.failed += source.failed;
  target.in += source.in;
  target.out += source.out;
  target.cache_read += source.cache_read;
  target.cache_write_5m += source.cache_write_5m;
  target.cache_write_1h += source.cache_write_1h;
  target.cost_usd += source.cost_usd;
  target.unpriced += source.unpriced;
  return target;
}

export const cacheWrites = (t: UsageTotals): number => t.cache_write_5m + t.cache_write_1h;

export const totalTokens = (t: UsageTotals): number =>
  t.in + t.out + t.cache_read + t.cache_write_5m + t.cache_write_1h;

export function modelFamily(model: string): ModelFamily {
  const m = model.toLowerCase();
  if (m.includes('opus')) return 'opus';
  if (m.includes('fable') || m.includes('mythos')) return 'fable';
  if (m.includes('sonnet')) return 'sonnet';
  if (m.includes('haiku')) return 'haiku';
  return 'other';
}

const FAMILY_NAMES: Record<string, string> = {
  opus: 'Opus',
  fable: 'Fable',
  mythos: 'Mythos',
  sonnet: 'Sonnet',
  haiku: 'Haiku',
};

/** `claude-opus-5-5` → `Opus 5.5`, `claude-sonnet-5` → `Sonnet 5`; other ids pass through. */
export function modelLabel(model: string): string {
  const match = /^claude-(opus|fable|mythos|sonnet|haiku)-(\d+)(?:-(\d{1,2}))?$/i.exec(
    model.trim()
  );
  if (!match) return model || '—';
  const [, family, major, minor] = match;
  return `${FAMILY_NAMES[family.toLowerCase()]} ${major}${minor ? `.${minor}` : ''}`;
}

export const scopeKey = (provider: string, account: string): string => `${provider}:${account}`;

export function rowsInScope(rows: UsageRow[], scope: AccountScope): UsageRow[] {
  if (scope === ALL_ACCOUNTS) return rows.filter((row) => row.provider === 'claude');
  return rows.filter((row) => scopeKey(row.provider, row.account) === scope);
}

export type AccountOption = { scope: AccountScope; provider: string; account: string };

/** Known accounts in the data, Claude first; the unknown ("") account is not selectable. */
export function accountOptions(rows: UsageRow[]): AccountOption[] {
  const seen = new Map<string, AccountOption>();
  for (const row of rows) {
    if (!row.account) continue;
    const scope = scopeKey(row.provider, row.account);
    if (!seen.has(scope)) seen.set(scope, { scope, provider: row.provider, account: row.account });
  }
  return [...seen.values()].sort((a, b) => {
    if (a.provider !== b.provider) {
      if (a.provider === 'claude') return -1;
      if (b.provider === 'claude') return 1;
      return a.provider.localeCompare(b.provider);
    }
    return a.account.localeCompare(b.account);
  });
}

export function sumRows(rows: UsageRow[]): UsageTotals {
  return rows.reduce((acc, row) => addTotals(acc, row), emptyTotals());
}

export type ModelSummary = { model: string; family: ModelFamily; totals: UsageTotals };

/** One entry per model, most expensive first (then most tokens). */
export function byModel(rows: UsageRow[]): ModelSummary[] {
  const map = new Map<string, ModelSummary>();
  for (const row of rows) {
    let entry = map.get(row.model);
    if (!entry) {
      entry = { model: row.model, family: modelFamily(row.model), totals: emptyTotals() };
      map.set(row.model, entry);
    }
    addTotals(entry.totals, row);
  }
  return [...map.values()].sort(
    (a, b) => b.totals.cost_usd - a.totals.cost_usd || totalTokens(b.totals) - totalTokens(a.totals)
  );
}

export type AccountSummary = { account: string; totals: UsageTotals };

/** One entry per account (unknown last), most expensive first. */
export function byAccount(rows: UsageRow[]): AccountSummary[] {
  const map = new Map<string, AccountSummary>();
  for (const row of rows) {
    let entry = map.get(row.account);
    if (!entry) {
      entry = { account: row.account, totals: emptyTotals() };
      map.set(row.account, entry);
    }
    addTotals(entry.totals, row);
  }
  return [...map.values()].sort((a, b) => {
    if (!a.account !== !b.account) return a.account ? -1 : 1;
    return b.totals.cost_usd - a.totals.cost_usd || totalTokens(b.totals) - totalTokens(a.totals);
  });
}

export type DayPoint = {
  day: string;
  byFamily: Record<ModelFamily, UsageTotals>;
  totals: UsageTotals;
  imported: boolean;
};

/** Every day of the range in order, empty days included. */
export function byDay(rows: UsageRow[], days: string[]): DayPoint[] {
  const points = new Map<string, DayPoint>();
  for (const day of days) {
    points.set(day, {
      day,
      byFamily: {
        opus: emptyTotals(),
        fable: emptyTotals(),
        sonnet: emptyTotals(),
        haiku: emptyTotals(),
        other: emptyTotals(),
      },
      totals: emptyTotals(),
      imported: false,
    });
  }
  for (const row of rows) {
    const point = points.get(row.day);
    if (!point) continue;
    addTotals(point.byFamily[modelFamily(row.model)], row);
    addTotals(point.totals, row);
    if (row.src === 'import') point.imported = true;
  }
  return days.map((day) => points.get(day)!);
}

/** `2026-10` → `{ from: '2026-10-01', to: '2026-11-01' }` (to is exclusive). */
export function monthRange(month: string): { from: string; to: string } {
  const [year, mon] = month.split('-').map(Number);
  const next = mon === 12 ? `${year + 1}-01` : `${year}-${String(mon + 1).padStart(2, '0')}`;
  return { from: `${month}-01`, to: `${next}-01` };
}

export function daysOfMonth(month: string): string[] {
  const [year, mon] = month.split('-').map(Number);
  const count = new Date(Date.UTC(year, mon, 0)).getUTCDate();
  return Array.from({ length: count }, (_, i) => `${month}-${String(i + 1).padStart(2, '0')}`);
}

export function shiftMonth(month: string, delta: number): string {
  const [year, mon] = month.split('-').map(Number);
  const index = year * 12 + (mon - 1) + delta;
  return `${Math.floor(index / 12)}-${String((index % 12) + 1).padStart(2, '0')}`;
}

export function currentMonth(now = new Date()): string {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`;
}

/** USD with cents; sub-cent amounts show as "<$0.01". */
export function formatUsd(value: number, locale?: string): string {
  if (value > 0 && value < 0.005) return '<$0.01';
  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value);
}

/**
 * Masked display names that stay distinguishable: when two accounts mask to the
 * same text (`g•••@example.com`), the end of the local part is kept as well.
 */
export function distinctMaskedNames(accounts: string[]): Map<string, string> {
  const masked = new Map(accounts.map((account) => [account, maskAccountName(account)]));
  const counts = new Map<string, number>();
  for (const name of masked.values()) counts.set(name, (counts.get(name) ?? 0) + 1);
  for (const [account, name] of masked) {
    const at = account.indexOf('@');
    if ((counts.get(name) ?? 0) < 2 || at < 4) continue;
    const local = account.slice(0, at);
    masked.set(account, `${local.slice(0, 1)}•••${local.slice(-2)}${account.slice(at)}`);
  }
  return masked;
}
