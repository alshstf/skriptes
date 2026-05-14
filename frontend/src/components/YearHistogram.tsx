import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import type { YearCount } from '@/lib/catalog';

/**
 * YearHistogram — bar chart распределения по году добавления книг.
 *
 * Используется на страницах автора и серии. Если данных меньше 2 точек —
 * блок прячется (см. условие в родителе), иначе одинокий столбик выглядит
 * глупо.
 *
 * recharts ResponsiveContainer берёт ширину родителя; высота фиксированная
 * чтобы layout страницы не прыгал во время загрузки.
 */
export function YearHistogram({ data }: { data: YearCount[] }) {
  // recharts ожидает массив объектов; наш YearCount уже подходит.
  // Year оставляем числом — recharts не любит "2023" как строку
  // на linear axis. На XAxis приводим к строке для подписи.
  return (
    <div className="h-48 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -24 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="oklch(0.92 0 0)" vertical={false} />
          <XAxis
            dataKey="year"
            tick={{ fontSize: 11, fill: 'oklch(0.556 0 0)' }}
            tickLine={false}
            axisLine={false}
          />
          <YAxis
            allowDecimals={false}
            tick={{ fontSize: 11, fill: 'oklch(0.556 0 0)' }}
            tickLine={false}
            axisLine={false}
            width={32}
          />
          <Tooltip
            cursor={{ fill: 'oklch(0.97 0 0)' }}
            contentStyle={{
              borderRadius: '8px',
              border: '1px solid oklch(0.92 0 0)',
              fontSize: '12px',
            }}
            labelFormatter={(label) => `${label} год`}
            formatter={(value) => [String(value), 'книг']}
          />
          <Bar dataKey="count" fill="oklch(0.205 0 0)" radius={[3, 3, 0, 0]} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
