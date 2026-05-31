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
import type { YearCount } from '@/lib/catalog';

/**
 * YearHistogram — bar chart распределения книг по годам написания.
 *
 * Используется на страницах автора и серии. Если данных меньше 2 точек —
 * блок прячется (см. условие в родителе), иначе одинокий столбик выглядит
 * глупо.
 *
 * Над каждым столбиком — число книг, слева — ось Y со счётчиком; при
 * наведении тултип показывает ЧТО за книги написаны в этот год (titles).
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
function pluralBooks(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod10 === 1 && mod100 !== 11) return 'книга';
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 10 || mod100 >= 20)) return 'книги';
  return 'книг';
}

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

export function YearHistogram({ data }: { data: YearCount[] }) {
  // recharts ожидает массив объектов; наш YearCount уже подходит.
  // Year оставляем числом — recharts не любит "2023" как строку
  // на linear axis. На XAxis приводим к строке для подписи.
  const c = useChartColors();
  // Числовая (линейная) ось X: расстояние между столбиками пропорционально
  // разрыву в годах (2007→2010→2015 не равноудалены). Тики — строго по
  // годам книг. domain паддим на полусреднего-разрыва, чтобы крайние бары
  // не упирались в границы.
  const years = data.map((d) => d.year);
  const minY = years.length ? Math.min(...years) : 0;
  const maxY = years.length ? Math.max(...years) : 1;
  const pad = years.length > 1 ? Math.max(1, Math.round((maxY - minY) / (years.length - 1) / 2)) : 1;
  const domain: [number, number] = [minY - pad, maxY + pad];
  return (
    <div className="h-48 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 16, right: 8, bottom: 0, left: 0 }}>
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
          <YAxis
            allowDecimals={false}
            tick={{ fontSize: 11, fill: c.tick }}
            tickLine={false}
            axisLine={false}
            width={28}
          />
          <Tooltip
            cursor={{ fill: c.cursor }}
            content={(props) => {
              const { active, payload, label } = props as unknown as TooltipRenderProps;
              const datum = payload?.[0]?.payload;
              if (!active || !datum) return null;
              return <YearTooltip year={label} datum={datum} colors={c} />;
            }}
          />
          <Bar
            dataKey="count"
            fill={c.bar}
            radius={[3, 3, 0, 0]}
            maxBarSize={56}
            isAnimationActive={false}
          >
            <LabelList dataKey="count" position="top" fill={c.label} fontSize={11} />
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
