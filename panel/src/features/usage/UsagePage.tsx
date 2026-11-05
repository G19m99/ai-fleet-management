import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { IconChevronLeft } from '@/components/ui/icons';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/Table';
import { formatCompactNumber, formatPercent } from '@/utils/format';
import { getErrorMessage } from '@/utils/helpers';
import { useRoutingStateStore } from '@/features/quota/routing/useRoutingState';
import { Meter } from '@/features/dashboard/components/Meter';
import { usageHistoryApi, type UsageHistory, type UsageTotals } from '@/services/api/usageHistory';
import dash from '@/features/dashboard/dashboard.module.scss';
import segmented from '@/features/config/components/ModeSwitch.module.scss';
import styles from './usage.module.scss';
import { DailyChart, type ChartMetric } from './DailyChart';
import {
  ALL_ACCOUNTS,
  accountOptions,
  byAccount,
  byDay,
  byModel,
  cacheWrites,
  currentMonth,
  daysOfMonth,
  distinctMaskedNames,
  formatUsd,
  modelLabel,
  monthRange,
  rowsInScope,
  shiftMonth,
  sumRows,
  totalTokens,
  type AccountScope,
  type ModelFamily,
} from './usageModel';

const DASH = '—';

const browserTimeZone = (): string | undefined => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || undefined;
  } catch {
    return undefined;
  }
};

const share = (part: number, whole: number): number | null =>
  whole > 0 ? (part / whole) * 100 : null;

export function UsagePage() {
  const { t, i18n } = useTranslation();
  const locale = i18n.language;
  const showEmails = useRoutingStateStore((state) => state.showEmails);

  const [month, setMonth] = useState(currentMonth);
  const [scope, setScope] = useState<AccountScope>(ALL_ACCOUNTS);
  const [metric, setMetric] = useState<ChartMetric>('cost');
  // undefined: loading; null: the server has no usage-history endpoint.
  const [data, setData] = useState<UsageHistory | null | undefined>(undefined);
  const [error, setError] = useState('');
  const requestId = useRef(0);

  const load = useCallback(async () => {
    const id = ++requestId.current;
    setError('');
    try {
      const result = await usageHistoryApi.get({ ...monthRange(month), tz: browserTimeZone() });
      if (id === requestId.current) setData(result);
    } catch (err: unknown) {
      if (id === requestId.current) setError(getErrorMessage(err) || t('usage.load_failed'));
    }
  }, [month, t]);

  useEffect(() => {
    void load();
  }, [load]);
  useHeaderRefresh(load);

  const rows = useMemo(() => data?.rows ?? [], [data]);
  const options = useMemo(() => accountOptions(rows), [rows]);
  const scoped = useMemo(() => rowsInScope(rows, scope), [rows, scope]);
  const totals = useMemo(() => sumRows(scoped), [scoped]);
  const models = useMemo(() => byModel(scoped), [scoped]);
  const accounts = useMemo(
    () => (scope === ALL_ACCOUNTS ? byAccount(scoped) : []),
    [scope, scoped]
  );
  const days = useMemo(() => byDay(scoped, daysOfMonth(month)), [scoped, month]);
  const hasImported = scoped.some((row) => row.src === 'import');

  const familyLabels: Record<ModelFamily, string> = {
    opus: 'Opus',
    fable: 'Fable',
    sonnet: 'Sonnet',
    haiku: 'Haiku',
    other: t('usage.family_other'),
  };

  const maskedNames = useMemo(
    () => distinctMaskedNames([...new Set(rows.map((row) => row.account).filter(Boolean))]),
    [rows]
  );
  const accountName = (provider: string, account: string): string => {
    if (!account) return t('usage.account_unknown');
    const name = showEmails ? account : (maskedNames.get(account) ?? account);
    return provider === 'claude' ? name : `${name} (${provider === 'codex' ? 'Codex' : provider})`;
  };

  const monthTitle = new Date(`${month}-15T12:00:00`).toLocaleDateString(locale, {
    month: 'long',
    year: 'numeric',
  });
  const earliest = data?.months?.[0];
  const canGoBack = !earliest || month > earliest;
  const canGoForward = month < currentMonth();

  const priced = totals.cost_usd > 0;
  const shareBasis = (x: UsageTotals) => (priced ? x.cost_usd : totalTokens(x));
  const top = models[0];
  const topShare = top ? share(shareBasis(top.totals), shareBasis(totals)) : null;
  const proxySince = data?.proxy_since
    ? new Date(data.proxy_since).toLocaleDateString(locale, { month: 'short', day: 'numeric' })
    : '';

  const tiles = [
    {
      key: 'cost',
      label: t('usage.tile_cost'),
      value: data ? formatUsd(totals.cost_usd, locale) : DASH,
      hint: totals.unpriced > 0 ? t('usage.tile_cost_hint_unpriced') : t('usage.tile_cost_hint'),
      accent: 'var(--border-hover)',
      meter: null as number | null,
    },
    {
      key: 'tokens',
      label: t('usage.tile_tokens'),
      value: data ? formatCompactNumber(totalTokens(totals)) : DASH,
      hint: t('usage.tile_tokens_hint', {
        input: formatCompactNumber(totals.in),
        output: formatCompactNumber(totals.out),
        cache: formatCompactNumber(totals.cache_read + cacheWrites(totals)),
      }),
      accent: 'var(--border-hover)',
      meter: null,
    },
    {
      key: 'requests',
      label: t('usage.tile_requests'),
      value: data ? totals.requests.toLocaleString(locale) : DASH,
      hint:
        totals.failed > 0
          ? t('usage.tile_requests_failed', { count: totals.failed })
          : t('usage.tile_requests_hint'),
      accent: 'var(--border-hover)',
      meter: null,
    },
    {
      key: 'top',
      label: t('usage.tile_top_model'),
      value: top ? modelLabel(top.model) : DASH,
      hint:
        topShare === null
          ? t('usage.no_usage')
          : t(priced ? 'usage.tile_top_hint_cost' : 'usage.tile_top_hint_tokens', {
              share: formatPercent(topShare),
            }),
      accent: top ? `var(--usage-${top.family})` : 'var(--border-hover)',
      meter: topShare,
    },
  ];

  return (
    <div className={`${dash.page} ${styles.page}`}>
      <header className={styles.header}>
        <h1 className={dash.pageTitle}>{t('nav.usage')}</h1>
        <div className={styles.controls}>
          <div className={styles.monthStepper} role="group" aria-label={t('usage.month')}>
            <button
              type="button"
              className={styles.monthButton}
              onClick={() => setMonth((m) => shiftMonth(m, -1))}
              disabled={!canGoBack}
              aria-label={t('usage.previous_month')}
            >
              <IconChevronLeft size={16} />
            </button>
            <span className={styles.monthLabel} aria-live="polite">
              {monthTitle}
            </span>
            <button
              type="button"
              className={styles.monthButton}
              onClick={() => setMonth((m) => shiftMonth(m, 1))}
              disabled={!canGoForward}
              aria-label={t('usage.next_month')}
            >
              <IconChevronLeft size={16} style={{ transform: 'rotate(180deg)' }} />
            </button>
          </div>
          <div
            className={`${segmented.segmented} ${styles.accountSwitch}`}
            role="group"
            aria-label={t('usage.account')}
          >
            {[{ scope: ALL_ACCOUNTS, provider: 'claude', account: '' }, ...options].map(
              (option) => (
                <button
                  key={option.scope}
                  type="button"
                  className={`${segmented.segment} ${scope === option.scope ? segmented.segmentActive : ''}`}
                  aria-pressed={scope === option.scope}
                  onClick={() => setScope(option.scope)}
                >
                  {option.scope === ALL_ACCOUNTS
                    ? t('usage.combined')
                    : accountName(option.provider, option.account)}
                </button>
              )
            )}
          </div>
        </div>
      </header>

      {data === null && <p className={styles.notice}>{t('usage.unsupported')}</p>}
      {data && !data.enabled && <p className={styles.notice}>{t('usage.disabled')}</p>}
      {error && <p className={styles.notice}>{error}</p>}

      <section className={dash.statsRow} aria-label={t('usage.stats_aria')}>
        {tiles.map((tile) => (
          <article
            key={tile.key}
            className={dash.statTile}
            style={{ '--tile-accent': tile.accent } as React.CSSProperties}
          >
            <span className={dash.statLabel}>{tile.label}</span>
            <strong className={dash.statValue}>{tile.value}</strong>
            {tile.meter !== null && (
              <Meter
                value={tile.meter}
                tone="idle"
                ariaLabel={tile.label}
                className={dash.statMeter}
              />
            )}
            <span className={dash.statHint}>{tile.hint}</span>
          </article>
        ))}
      </section>

      <section className={dash.section}>
        <header className={dash.sectionHeadRow}>
          <div className={dash.sectionHead}>
            <h2 className={dash.sectionTitle}>{t('usage.daily_title')}</h2>
            <p className={dash.sectionDescription}>
              {t(
                metric === 'cost'
                  ? 'usage.daily_description_cost'
                  : 'usage.daily_description_tokens'
              )}
            </p>
          </div>
          <div className={segmented.segmented} role="group" aria-label={t('usage.metric')}>
            {(['cost', 'tokens'] as const).map((value) => (
              <button
                key={value}
                type="button"
                className={`${segmented.segment} ${metric === value ? segmented.segmentActive : ''}`}
                aria-pressed={metric === value}
                onClick={() => setMetric(value)}
              >
                {t(value === 'cost' ? 'usage.metric_cost' : 'usage.metric_tokens')}
              </button>
            ))}
          </div>
        </header>
        <div className={dash.panel}>
          <DailyChart points={days} metric={metric} familyLabels={familyLabels} locale={locale} />
        </div>
      </section>

      <section className={dash.section}>
        <header className={dash.sectionHead}>
          <h2 className={dash.sectionTitle}>{t('usage.models_title')}</h2>
          <p className={dash.sectionDescription}>{t('usage.models_description')}</p>
        </header>
        {models.length === 0 ? (
          <div className={dash.panel}>
            <p className={styles.empty}>
              {data === undefined ? t('usage.loading') : t('usage.no_usage')}
            </p>
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('usage.col_model')}</TableHead>
                <TableHead alignRight>{t('usage.col_requests')}</TableHead>
                <TableHead alignRight>{t('usage.col_input')}</TableHead>
                <TableHead alignRight>{t('usage.col_output')}</TableHead>
                <TableHead alignRight>{t('usage.col_cache_read')}</TableHead>
                <TableHead alignRight>{t('usage.col_cache_write')}</TableHead>
                <TableHead alignRight>{t('usage.col_cost')}</TableHead>
                <TableHead alignRight>{t('usage.col_share')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {models.map((entry) => {
                const pct = share(shareBasis(entry.totals), shareBasis(totals));
                return (
                  <TableRow key={entry.model}>
                    <TableCell>
                      <span className={styles.modelCell}>
                        <span
                          className={`${styles.swatch} ${styles[`family-${entry.family}`]}`}
                          aria-hidden="true"
                        />
                        {modelLabel(entry.model)}
                      </span>
                    </TableCell>
                    <TableCell alignRight className={styles.num}>
                      {entry.totals.requests.toLocaleString(locale)}
                    </TableCell>
                    <TableCell alignRight className={styles.num}>
                      {formatCompactNumber(entry.totals.in)}
                    </TableCell>
                    <TableCell alignRight className={styles.num}>
                      {formatCompactNumber(entry.totals.out)}
                    </TableCell>
                    <TableCell alignRight className={styles.num}>
                      {formatCompactNumber(entry.totals.cache_read)}
                    </TableCell>
                    <TableCell alignRight className={styles.num}>
                      {formatCompactNumber(cacheWrites(entry.totals))}
                    </TableCell>
                    <TableCell alignRight className={styles.num}>
                      {entry.totals.unpriced > 0 && entry.totals.cost_usd === 0 ? (
                        <span className={styles.muted} title={t('usage.unpriced_title')}>
                          {DASH}
                        </span>
                      ) : (
                        formatUsd(entry.totals.cost_usd, locale)
                      )}
                    </TableCell>
                    <TableCell alignRight>
                      <span className={styles.shareCell}>
                        <span className={styles.num}>
                          {pct === null ? DASH : formatPercent(pct)}
                        </span>
                        <Meter
                          value={pct}
                          tone="idle"
                          ariaLabel={t('usage.col_share')}
                          className={styles.shareMeter}
                        />
                      </span>
                    </TableCell>
                  </TableRow>
                );
              })}
              <TableRow className={styles.totalRow}>
                <TableCell>{t('usage.total')}</TableCell>
                <TableCell alignRight className={styles.num}>
                  {totals.requests.toLocaleString(locale)}
                </TableCell>
                <TableCell alignRight className={styles.num}>
                  {formatCompactNumber(totals.in)}
                </TableCell>
                <TableCell alignRight className={styles.num}>
                  {formatCompactNumber(totals.out)}
                </TableCell>
                <TableCell alignRight className={styles.num}>
                  {formatCompactNumber(totals.cache_read)}
                </TableCell>
                <TableCell alignRight className={styles.num}>
                  {formatCompactNumber(cacheWrites(totals))}
                </TableCell>
                <TableCell alignRight className={styles.num}>
                  {formatUsd(totals.cost_usd, locale)}
                </TableCell>
                <TableCell />
              </TableRow>
            </TableBody>
          </Table>
        )}
      </section>

      {scope === ALL_ACCOUNTS && accounts.length > 0 && (
        <section className={dash.section}>
          <header className={dash.sectionHead}>
            <h2 className={dash.sectionTitle}>{t('usage.accounts_title')}</h2>
            <p className={dash.sectionDescription}>{t('usage.accounts_description')}</p>
          </header>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('usage.col_account')}</TableHead>
                <TableHead alignRight>{t('usage.col_requests')}</TableHead>
                <TableHead alignRight>{t('usage.col_tokens')}</TableHead>
                <TableHead alignRight>{t('usage.col_cost')}</TableHead>
                <TableHead alignRight>{t('usage.col_share')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {accounts.map((entry) => {
                const pct = share(shareBasis(entry.totals), shareBasis(totals));
                return (
                  <TableRow key={entry.account || '-'}>
                    <TableCell>
                      <span className={styles.modelCell}>
                        {accountName('claude', entry.account)}
                      </span>
                      {!entry.account && (
                        <span className={styles.accountHint}>
                          {t('usage.account_unknown_hint')}
                        </span>
                      )}
                    </TableCell>
                    <TableCell alignRight className={styles.num}>
                      {entry.totals.requests.toLocaleString(locale)}
                    </TableCell>
                    <TableCell alignRight className={styles.num}>
                      {formatCompactNumber(totalTokens(entry.totals))}
                    </TableCell>
                    <TableCell alignRight className={styles.num}>
                      {formatUsd(entry.totals.cost_usd, locale)}
                    </TableCell>
                    <TableCell alignRight>
                      <span className={styles.shareCell}>
                        <span className={styles.num}>
                          {pct === null ? DASH : formatPercent(pct)}
                        </span>
                        <Meter
                          value={pct}
                          tone="idle"
                          ariaLabel={t('usage.col_share')}
                          className={styles.shareMeter}
                        />
                      </span>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </section>
      )}

      <p className={styles.footnote}>{t('usage.footnote_cost')}</p>
      {hasImported && (
        <p className={styles.footnote}>
          {proxySince
            ? t('usage.footnote_imported_since', { date: proxySince })
            : t('usage.footnote_imported')}
        </p>
      )}
    </div>
  );
}
