import { Download } from 'lucide-react';

/**
 * ReadingProgress — "Скачано N из M книг" + горизонтальный прогресс-бар.
 *
 * Используется на страницах автора и серии. "Скачано" — потому что
 * настоящий "прочитано" (через completed_at) появится только когда
 * в Фазе 3 будет in-browser reader. До тех пор скачивание — лучший
 * прокси для интереса.
 *
 * Если total = 0 — компонент ничего не рендерит (родитель тоже не
 * должен его вставлять, но дублирующая защита не повредит).
 */
export function ReadingProgress({
  read,
  total,
}: {
  read: number;
  total: number;
}) {
  if (total <= 0) return null;
  const pct = Math.min(100, Math.round((read / total) * 100));
  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between text-sm">
        <span className="flex items-center gap-1.5 text-muted-foreground">
          <Download className="size-3.5" aria-hidden />
          Скачано {read} из {total} {pluralBooks(total)}
        </span>
        <span className="tabular-nums text-xs text-muted-foreground">{pct}%</span>
      </div>
      <div
        className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
        role="progressbar"
        aria-valuenow={read}
        aria-valuemax={total}
        aria-label="Прогресс чтения"
      >
        <div
          className="h-full rounded-full bg-primary transition-all"
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  );
}

function pluralBooks(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'книг';
  if (mod10 === 1) return 'книги';
  if (mod10 >= 2 && mod10 <= 4) return 'книг';
  return 'книг';
}
