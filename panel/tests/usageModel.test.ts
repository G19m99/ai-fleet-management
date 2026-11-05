import { describe, expect, test } from 'bun:test';
import type { UsageRow } from '@/services/api/usageHistory';
import {
  ALL_ACCOUNTS,
  accountOptions,
  byAccount,
  byDay,
  byModel,
  daysOfMonth,
  distinctMaskedNames,
  formatUsd,
  modelFamily,
  modelLabel,
  monthRange,
  rowsInScope,
  scopeKey,
  shiftMonth,
  sumRows,
  totalTokens,
} from '@/features/usage/usageModel';

const row = (overrides: Partial<UsageRow>): UsageRow => ({
  day: '2026-10-21',
  src: 'proxy',
  provider: 'claude',
  account: 'a@x.com',
  model: 'claude-opus-5-5',
  requests: 1,
  failed: 0,
  in: 10,
  out: 20,
  cache_read: 30,
  cache_write_5m: 0,
  cache_write_1h: 40,
  cost_usd: 1,
  unpriced: 0,
  ...overrides,
});

const rows: UsageRow[] = [
  row({}),
  row({ account: 'b@x.com', model: 'claude-fable-5-1', cost_usd: 5 }),
  row({ account: '', src: 'import', day: '2026-10-02', model: 'claude-sonnet-5-5', cost_usd: 0.5 }),
  row({ provider: 'codex', account: 'a@x.com', model: 'gpt-5.5-codex', cost_usd: 0, unpriced: 1 }),
];

describe('model names', () => {
  test('labels Claude ids and passes others through', () => {
    expect(modelLabel('claude-opus-5-5')).toBe('Opus 5.5');
    expect(modelLabel('claude-sonnet-5')).toBe('Sonnet 5');
    expect(modelLabel('claude-haiku-4-5')).toBe('Haiku 4.5');
    expect(modelLabel('gpt-5.5-codex')).toBe('gpt-5.5-codex');
  });

  test('families group by line, mythos with fable', () => {
    expect(modelFamily('claude-mythos-5-1')).toBe('fable');
    expect(modelFamily('claude-haiku-5-5')).toBe('haiku');
    expect(modelFamily('gpt-5.5-codex')).toBe('other');
  });
});

describe('scopes', () => {
  test('combined covers every Claude account, including unknown, but not Codex', () => {
    const scoped = rowsInScope(rows, ALL_ACCOUNTS);
    expect(scoped).toHaveLength(3);
    expect(sumRows(scoped).cost_usd).toBeCloseTo(6.5);
  });

  test('one account is matched by provider and email', () => {
    expect(rowsInScope(rows, scopeKey('claude', 'a@x.com'))).toHaveLength(1);
    expect(rowsInScope(rows, scopeKey('codex', 'a@x.com'))[0].model).toBe('gpt-5.5-codex');
  });

  test('account options list Claude first and skip the unknown account', () => {
    expect(accountOptions(rows).map((o) => o.scope)).toEqual([
      'claude:a@x.com',
      'claude:b@x.com',
      'codex:a@x.com',
    ]);
  });
});

describe('grouping', () => {
  test('byModel sorts by cost', () => {
    expect(byModel(rowsInScope(rows, ALL_ACCOUNTS)).map((m) => m.model)).toEqual([
      'claude-fable-5-1',
      'claude-opus-5-5',
      'claude-sonnet-5-5',
    ]);
  });

  test('byAccount puts the unknown account last', () => {
    expect(byAccount(rowsInScope(rows, ALL_ACCOUNTS)).map((a) => a.account)).toEqual([
      'b@x.com',
      'a@x.com',
      '',
    ]);
  });

  test('byDay fills every day and flags imported days', () => {
    const days = byDay(rowsInScope(rows, ALL_ACCOUNTS), daysOfMonth('2026-10'));
    expect(days).toHaveLength(31);
    expect(days[1].imported).toBe(true);
    expect(days[1].byFamily.sonnet.cost_usd).toBeCloseTo(0.5);
    expect(days[20].totals.cost_usd).toBeCloseTo(6);
    expect(days[0].totals.requests).toBe(0);
  });

  test('totalTokens sums every bucket', () => {
    expect(totalTokens(row({}))).toBe(100);
  });
});

describe('months', () => {
  test('ranges and shifting cross year boundaries', () => {
    expect(monthRange('2026-12')).toEqual({ from: '2026-12-01', to: '2027-01-01' });
    expect(shiftMonth('2026-01', -1)).toBe('2025-12');
    expect(shiftMonth('2026-11', 2)).toBe('2027-01');
    expect(daysOfMonth('2028-02')).toHaveLength(29);
  });

  test('formatUsd keeps cents and marks sub-cent spend', () => {
    expect(formatUsd(1234.5, 'en-US')).toBe('$1,234.50');
    expect(formatUsd(0.001, 'en-US')).toBe('<$0.01');
    expect(formatUsd(0, 'en-US')).toBe('$0.00');
  });
});

describe('distinctMaskedNames', () => {
  test('keeps the end of the local part when masks collide', () => {
    const names = distinctMaskedNames(['gershy@x.com', 'gershy_claude@x.com', 'alice@y.com']);
    expect(names.get('gershy@x.com')).toBe('g•••hy@x.com');
    expect(names.get('gershy_claude@x.com')).toBe('g•••de@x.com');
    expect(names.get('alice@y.com')).toBe('a•••@y.com');
  });
});
