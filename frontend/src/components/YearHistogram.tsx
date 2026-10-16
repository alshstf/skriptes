import { useEffect, useState } from 'react';
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  LabelList,
} from 'recharts';
import { Button } from '@/components/ui/button';
import type { YearCount } from '@/lib/catalog';
import { pluralBooks } from '@/lib/format';
import { useContainerWidth } from '@/lib/useContainerWidth';
import { binLabel, binStep, binYears, useHistogramMode, visibleLabels, type YearBin } from '@/lib/yearHistogram';

/**
 * YearHistogram — bar chart распределения книг по годам написания.
 *
 * Используется на страницах автора и серии. Если данных меньше 2 точек —
 * блок прячется (см. условие в родителе), иначе одинокий столбик выглядит
 * глупо.
 *
 * Над столбиками — число книг (в плотной — только не налезающие, #530), слева —
 * ось Y со счётчиком; при наведении тултип показывает ЧТО за книги написаны в
 * этот год или период (titles). Режим «Годы / Периоды» — переключателем
 * рядом с заголовком (lib/yearHistogram.ts).
 *
 * Цвета НЕ хардкодим — берём из активной темы (CSS-переменные shadcn).
 * Раньше тут были захардкожены светлые oklch (чёрные бары, светлая сетка):
 * приложение всегда в .dark (класс прибит в index.html), поэтому бары
 * сливались с фоном. Читаем переменные через getComputedStyle и
 * перечитываем при смене класса на <html> — на случай будущего
 * переключателя light/dark. Конкретная строка oklch(...) надёжнее, чем
 * fill="var(--…)" в SVG-атрибуте: var() в presentation-атрибуте резолвится
 * не во всех браузерах (старый WebKit / iOS).
 */
type ChartColors = {
  bar: string;
  grid: string;
  tick: string;
  label: string;
  cursor: string;
  tooltipBg: string;
  tooltipBorder: string;
  tooltipText: string;
};

// Фоллбэк = значения .dark-темы: используется в jsdom (getComputedStyle не
// отдаёт кастомные свойства) и до первого чтения.
const FALLBACK: ChartColors = {
  bar: 'oklch(0.922 0 0)',
  grid: 'oklch(1 0 0 / 10%)',
  tick: 'oklch(0.708 0 0)',
  label: 'oklch(0.985 0 0)',
  cursor: 'oklch(1 0 0 / 8%)',
  tooltipBg: 'oklch(0.205 0 0)',
  tooltipBorder: 'oklch(1 0 0 / 10%)',
  tooltipText: 'oklch(0.985 0 0)',
};

function readChartColors(): ChartColors {
  if (typeof document === 'undefined') return FALLBACK;
  const s = getComputedStyle(document.documentElement);
  const v = (name: string, fb: string) => s.getPropertyValue(name).trim() || fb;
  return {
    bar: v('--primary', FALLBACK.bar),
    grid: v('--border', FALLBACK.grid),
    tick: v('--muted-foreground', FALLBACK.tick),
    label: v('--foreground', FALLBACK.label),
    cursor: v('--accent', FALLBACK.cursor),
    tooltipBg: v('--popover', FALLBACK.tooltipBg),
    tooltipBorder: v('--border', FALLBACK.tooltipBorder),
    tooltipText: v('--popover-foreground', FALLBACK.tooltipText),
  };
}

function useChartColors(): ChartColors {
  const [colors, setColors] = useState<ChartColors>(readChartColors);
  useEffect(() => {
    const obs = new MutationObserver(() => setColors(readChartColors()));
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    return () => obs.disconnect();
  }, []);
  return colors;
}

// pluralBooks — русское склонение «книга / книги / книг».

const TOOLTIP_BOOKS_LIMIT = 8;

function YearTooltip({
  year,
  datum,
  colors,
}: {
  year: string | number | undefined;
  datum: YearCount;
  colors: ChartColors;
}) {
  const books = datum.books ?? [];
  const shown = books.slice(0, TOOLTIP_BOOKS_LIMIT);
  const rest = datum.count - shown.length;
  return (
    <div
      style={{
        background: colors.tooltipBg,
        border: `1px solid ${colors.tooltipBorder}`,
        color: colors.tooltipText,
        borderRadius: 8,
        padding: '8px 10px',
        fontSize: 12,
        maxWidth: 280,
        boxShadow: '0 2px 10px rgba(0,0,0,0.4)',
      }}
    >
      <div style={{ fontWeight: 600, marginBottom: shown.length ? 6 : 0 }}>
        {year} · {datum.count} {pluralBooks(datum.count)}
      </div>
      {shown.length > 0 ? (
        <ul style={{ margin: 0, padding: 0, listStyle: 'none', display: 'grid', gap: 3 }}>
          {shown.map((b) => (
            <li key={b.id} style={{ lineHeight: 1.3 }}>
              {b.title}
            </li>
          ))}
          {rest > 0 ? <li style={{ opacity: 0.6 }}>…и ещё {rest}</li> : null}
        </ul>
      ) : null}
    </div>
  );
}

// Структурный тип того, что recharts передаёт в content-рендер тултипа —
// чтобы не тянуть generic-типы recharts (в v3 их расположение меняется).
type TooltipRenderProps = {
  active?: boolean;
  label?: string | number;
  payload?: Array<{ payload?: YearCount }>;
};

const Y_AXIS_W = 28;
const MARGIN_R = 8;

/**
 * YearHistogram с заголовком и переключателем режима «Годы / Периоды»
 * (#530, lib/yearHistogram.ts). Переключатель — только когда периоды что-то
 * меняют (по ширине нужны корзины); иначе — столбик на год.
 */
export function YearHistogram({ data, title }: { data: YearCount[]; title: string }) {
  const c = useChartColors();
  const [ref, width] = useContainerWidth<HTMLDivElement>();
  const [mode, setMode] = useHistogramMode();
  const plotWidth = Math.max(0, width - Y_AXIS_W - MARGIN_R);
  const step = binStep(data, plotWidth);
  const periods = step > 1 && mode === 'periods';
  return (
    <div className="space-y-1">
      <div className="flex min-h-7 flex-wrap items-center justify-between gap-x-2 gap-y-1">
        <div className="text-xs font-medium uppercase text-muted-foreground">{title}</div>
        {step > 1 ? (
          <div className="ml-auto flex shrink-0 gap-1" role="group" aria-label="Столбики гистограммы">
            {(
              [
                ['years', 'Годы'],
                ['periods', 'Периоды'],
              ] as const
            ).map(([m, label]) => (
              <Button
                key={m}
                size="sm"
                variant={(m === 'periods') === periods ? 'default' : 'outline'}
                aria-pressed={(m === 'periods') === periods}
                className="h-7 px-2 text-xs"
                onClick={() => setMode(m)}
              >
                {label}
              </Button>
            ))}
          </div>
        ) : null}
      </div>
      <div ref={ref} className="h-48 w-full" data-testid="year-histogram" data-mode={periods ? 'periods' : 'years'}>
        {periods ? (
          <BinnedChart bins={binYears(data, step)} colors={c} />
        ) : (
          <YearChart data={data} plotWidth={plotWidth} colors={c} />
        )}
      </div>
    </div>
  );
}

function tooltipContent(c: ChartColors, title: (d: YearCount) => string | number | undefined) {
  return (props: unknown) => {
    const { active, payload } = props as TooltipRenderProps;
    const datum = payload?.[0]?.payload;
    if (!active || !datum) return null;
    return <YearTooltip year={title(datum)} datum={datum} colors={c} />;
  };
}

function YearChart({ data, plotWidth, colors: c }: { data: YearCount[]; plotWidth: number; colors: ChartColors }) {
  // Числовая (линейная) ось X: расстояние между столбиками пропорционально
  // разрыву в годах (2007→2010→2015 не равноудалены). Тики — строго по
  // годам книг. domain паддим на полусреднего-разрыва, чтобы крайние бары
  // не упирались в границы.
  const years = data.map((d) => d.year);
  const minY = years.length ? Math.min(...years) : 0;
  const maxY = years.length ? Math.max(...years) : 1;
  const pad = years.length > 1 ? Math.max(1, Math.round((maxY - minY) / (years.length - 1) / 2)) : 1;
  const domain: [number, number] = [minY - pad, maxY + pad];
  const xs = years.map((y) => ((y - domain[0]) / (domain[1] - domain[0])) * plotWidth);
  const shown = plotWidth > 0 ? visibleLabels(xs, data.map((d) => d.count)) : new Set(data.map((_, i) => i));
  return (
    <ResponsiveContainer width="100%" height="100%">
      <BarChart data={data} margin={{ top: 16, right: MARGIN_R, bottom: 0, left: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke={c.grid} vertical={false} />
        <XAxis
          dataKey="year"
          type="number"
          scale="linear"
          domain={domain}
          ticks={years}
          allowDecimals={false}
          tick={{ fontSize: 11, fill: c.tick }}
          tickLine={false}
          axisLine={false}
        />
        <YAxis allowDecimals={false} tick={{ fontSize: 11, fill: c.tick }} tickLine={false} axisLine={false} width={Y_AXIS_W} />
        <Tooltip cursor={{ fill: c.cursor }} content={tooltipContent(c, (d) => d.year)} />
        <Bar dataKey="count" fill={c.bar} radius={[3, 3, 0, 0]} maxBarSize={56} isAnimationActive={false}>
          <LabelList
            dataKey="count"
            position="top"
            fill={c.label}
            fontSize={11}
            content={(p) => {
              const { x, y, width: w, value, index } = p as { x?: number; y?: number; width?: number; value?: number; index?: number };
              if (index == null || !shown.has(index) || x == null || y == null) return null;
              return (
                <text x={Number(x) + Number(w ?? 0) / 2} y={Number(y) - 4} fill={c.label} fontSize={11} textAnchor="middle">
                  {value}
                </text>
              );
            }}
          />
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}

function BinnedChart({ bins, colors: c }: { bins: YearBin[]; colors: ChartColors }) {
  return (
    <ResponsiveContainer width="100%" height="100%">
      <BarChart data={bins} margin={{ top: 16, right: MARGIN_R, bottom: 0, left: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke={c.grid} vertical={false} />
        <XAxis
          dataKey="year"
          type="category"
          tickFormatter={(y: number) => String(y)}
          tick={{ fontSize: 11, fill: c.tick }}
          tickLine={false}
          axisLine={false}
          minTickGap={4}
        />
        <YAxis allowDecimals={false} tick={{ fontSize: 11, fill: c.tick }} tickLine={false} axisLine={false} width={Y_AXIS_W} />
        <Tooltip cursor={{ fill: c.cursor }} content={tooltipContent(c, (d) => binLabel(d as YearBin))} />
        <Bar dataKey="count" fill={c.bar} radius={[3, 3, 0, 0]} maxBarSize={56} isAnimationActive={false}>
          <LabelList
            dataKey="count"
            position="top"
            fill={c.label}
            fontSize={11}
            content={(p) => {
              const { x, y, width: w, value } = p as { x?: number; y?: number; width?: number; value?: number };
              if (!value || x == null || y == null) return null;
              return (
                <text x={Number(x) + Number(w ?? 0) / 2} y={Number(y) - 4} fill={c.label} fontSize={11} textAnchor="middle">
                  {value}
                </text>
              );
            }}
          />
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}
