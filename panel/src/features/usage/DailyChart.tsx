import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatCompactNumber } from '@/utils/format';
import { axisMax } from '@/features/dashboard/utils';
import chart from '@/features/dashboard/components/ThroughputChart.module.scss';
import styles from './usage.module.scss';
import {
  FAMILY_ORDER,
  formatUsd,
  totalTokens,
  type DayPoint,
  type ModelFamily,
} from './usageModel';
import type { UsageTotals } from '@/services/api/usageHistory';

export type ChartMetric = 'cost' | 'tokens';

const TICK_COUNT = 5;

const metricValue = (totals: UsageTotals, metric: ChartMetric): number =>
  metric === 'cost' ? totals.cost_usd : totalTokens(totals);

type DailyChartProps = {
  points: DayPoint[];
  metric: ChartMetric;
  familyLabels: Record<ModelFamily, string>;
  locale: string;
};

/** Stacked daily bars by model family; hover or tap a day for its breakdown. */
export function DailyChart({ points, metric, familyLabels, locale }: DailyChartProps) {
  const { t } = useTranslation();
  const [activeIndex, setActiveIndex] = useState<number | null>(null);

  const format = (value: number, axis = false): string => {
    if (metric === 'tokens') return formatCompactNumber(value);
    if (axis) return `$${formatCompactNumber(value)}`;
    return formatUsd(value, locale);
  };

  const values = useMemo(() => points.map((p) => metricValue(p.totals, metric)), [points, metric]);
  const peak = Math.max(0, ...values);
  const peakIndex = peak > 0 ? values.indexOf(peak) : -1;
  const scaleMax = axisMax(peak, TICK_COUNT - 1);
  const ticks = Array.from({ length: TICK_COUNT }, (_, i) => {
    const ratio = 1 - i / (TICK_COUNT - 1);
    return { ratio, value: scaleMax * ratio };
  });
  const familiesPresent = FAMILY_ORDER.filter((family) =>
    points.some((p) => metricValue(p.byFamily[family], metric) > 0)
  );
  const xTicks = points
    .map((p, index) => ({ index, label: String(Number(p.day.slice(8))) }))
    // Weekly ticks plus the last day, unless it would crowd the previous tick.
    .filter(({ index }) => index % 7 === 0 || (index === points.length - 1 && index % 7 >= 3));

  const dayLabel = (day: string) =>
    new Date(`${day}T12:00:00`).toLocaleDateString(locale, {
      weekday: 'short',
      month: 'short',
      day: 'numeric',
    });

  const active = activeIndex === null ? null : points[activeIndex];
  const summary = t('usage.chart_summary', {
    days: points.length,
    total: format(values.reduce((a, b) => a + b, 0)),
  });

  return (
    <figure className={chart.chart}>
      <figcaption className={chart.legend}>
        {familiesPresent.map((family) => (
          <span key={family} className={chart.legendItem}>
            <span
              className={`${chart.legendSwatch} ${styles[`family-${family}`]}`}
              aria-hidden="true"
            />
            {familyLabels[family]}
          </span>
        ))}
      </figcaption>

      <div className={chart.plot}>
        <div className={chart.yAxis} aria-hidden="true">
          {ticks.map((tick) => (
            <span
              key={tick.ratio}
              className={chart.yTick}
              style={{ top: `${(1 - tick.ratio) * 100}%` }}
            >
              {format(tick.value, true)}
            </span>
          ))}
        </div>

        <div className={chart.canvas}>
          <div className={chart.gridlines} aria-hidden="true">
            {ticks.map((tick) => (
              <span
                key={tick.ratio}
                className={chart.gridline}
                style={{ top: `${(1 - tick.ratio) * 100}%` }}
              />
            ))}
          </div>

          <div
            className={chart.columns}
            role="img"
            aria-label={summary}
            onMouseLeave={() => setActiveIndex(null)}
          >
            {points.map((point, index) => {
              const stacked = FAMILY_ORDER.map((family) => ({
                family,
                value: metricValue(point.byFamily[family], metric),
              })).filter((s) => s.value > 0);
              const topFirst = [...stacked].reverse();
              const delayMs =
                points.length > 1 ? Math.round((index / (points.length - 1)) * 360) : 0;
              return (
                <div
                  key={point.day}
                  className={`${chart.column} ${activeIndex === index ? chart.columnActive : ''}`}
                  onMouseEnter={() => setActiveIndex(index)}
                  onClick={() => setActiveIndex((current) => (current === index ? null : index))}
                >
                  {index === peakIndex && (
                    <span
                      className={chart.peakLabel}
                      style={{ bottom: `${Math.min(100, (peak / scaleMax) * 100)}%` }}
                    >
                      {format(peak)}
                    </span>
                  )}
                  <div
                    className={`${chart.stack} ${styles.stack}`}
                    style={{ '--bar-delay': `${delayMs}ms` } as React.CSSProperties}
                  >
                    {topFirst.map((segment, i) => (
                      <span
                        key={segment.family}
                        className={`${chart.segment} ${styles[`family-${segment.family}`]} ${
                          i === 0 ? styles.segmentTop : styles.segmentFlat
                        }`}
                        style={{ height: `${(segment.value / scaleMax) * 100}%` }}
                      />
                    ))}
                    {stacked.length === 0 && <span className={chart.idleTick} />}
                  </div>
                  {point.imported && <span className={styles.importedTick} aria-hidden="true" />}
                </div>
              );
            })}
          </div>

          {peak === 0 && <p className={chart.noRequests}>{t('usage.no_usage')}</p>}

          {active && activeIndex !== null && (
            <div
              className={`${chart.tooltip} ${styles.tooltip}`}
              style={{
                left: `${((activeIndex + 0.5) / points.length) * 100}%`,
                transform:
                  activeIndex < points.length * 0.15
                    ? 'translateX(-12%)'
                    : activeIndex > points.length * 0.85
                      ? 'translateX(-88%)'
                      : 'translateX(-50%)',
              }}
              role="status"
            >
              <span className={chart.tooltipTime}>{dayLabel(active.day)}</span>
              {FAMILY_ORDER.filter(
                (family) => metricValue(active.byFamily[family], metric) > 0
              ).map((family) => (
                <span key={family} className={chart.tooltipRow}>
                  <span
                    className={`${chart.legendSwatch} ${styles[`family-${family}`]}`}
                    aria-hidden="true"
                  />
                  {familyLabels[family]}
                  <span className={styles.tooltipValue}>
                    {format(metricValue(active.byFamily[family], metric))}
                  </span>
                </span>
              ))}
              <span className={styles.tooltipTotal}>
                {t('usage.total')}
                <span>{format(metricValue(active.totals, metric))}</span>
              </span>
              <span className={styles.tooltipNote}>
                {t('usage.requests_count', {
                  count: active.totals.requests,
                  value: active.totals.requests.toLocaleString(locale),
                })}
                {active.imported ? ` · ${t('usage.imported_day')}` : ''}
              </span>
            </div>
          )}
        </div>
      </div>

      <div className={chart.xAxis} aria-hidden="true">
        {xTicks.map((tick) => (
          <span
            key={tick.index}
            className={chart.xTick}
            style={{ left: `${((tick.index + 0.5) / points.length) * 100}%` }}
          >
            {tick.label}
          </span>
        ))}
      </div>
    </figure>
  );
}
