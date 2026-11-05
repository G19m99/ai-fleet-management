/**
 * Usage history (fork-only endpoint): per-day token usage and API-equivalent
 * cost, split by account and model.
 *
 * `GET /observability/usage-history?from=YYYY-MM-DD&to=YYYY-MM-DD&tz=Area/City`
 * (`to` is exclusive). Returns null on a 404 so the page can say the server
 * predates the feature.
 */

import type { ApiError } from '@/types';
import { apiClient } from './client';

export type UsageSource = 'proxy' | 'import';

export type UsageTotals = {
  requests: number;
  failed: number;
  in: number;
  out: number;
  cache_read: number;
  cache_write_5m: number;
  cache_write_1h: number;
  cost_usd: number;
  /** Requests whose model has no price; their cost is not included. */
  unpriced: number;
};

export type UsageRow = UsageTotals & {
  day: string;
  src: UsageSource;
  provider: string;
  /** Account email; empty when the account is unknown. */
  account: string;
  model: string;
};

export type UsageHistory = {
  enabled: boolean;
  from: string;
  to: string;
  tz: string;
  months: string[];
  proxy_since?: string;
  rows: UsageRow[];
};

const isApiError = (err: unknown): err is ApiError =>
  typeof err === 'object' && err !== null && 'status' in err;

export const usageHistoryApi = {
  async get(params: { from: string; to: string; tz?: string }): Promise<UsageHistory | null> {
    try {
      const data = await apiClient.get<UsageHistory>('/observability/usage-history', { params });
      return {
        enabled: Boolean(data?.enabled),
        from: data?.from ?? params.from,
        to: data?.to ?? params.to,
        tz: data?.tz ?? params.tz ?? '',
        months: Array.isArray(data?.months) ? data.months : [],
        proxy_since: data?.proxy_since || undefined,
        rows: Array.isArray(data?.rows) ? data.rows : [],
      };
    } catch (err: unknown) {
      if (isApiError(err) && err.status === 404) return null;
      throw err;
    }
  },
};
