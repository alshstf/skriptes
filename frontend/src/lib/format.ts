/**
 * lib/format.ts — общие форматтеры отображения.
 */

/**
 * formatBytes — человекочитаемый размер (Б/КБ/МБ/ГБ/ТБ, ru). `-1` (неизвестно)
 * → «—». Используется на карточке книги (размер издания) и в админке (кэши).
 */
export function formatBytes(n: number): string {
  if (n < 0) return '—';
  if (n < 1024) return `${n} Б`;
  const units = ['КБ', 'МБ', 'ГБ', 'ТБ'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)} ${units[i]}`;
}

/**
 * shortPersonName — «Гинзбург Юлия Александровна» → «Гинзбург Ю. А.».
 * Сокращаем ТОЛЬКО похожее на ФИО (2–3 слова с заглавной, каждое ≥2 букв):
 * «Любительский / сетевой перевод» или «Клименко Л» возвращаются как есть.
 */
export function shortPersonName(full: string): string {
  const parts = full.trim().split(/\s+/);
  const nameLike =
    parts.length >= 2 &&
    parts.length <= 3 &&
    parts.every((p) => /^[А-ЯЁA-Z][а-яёa-zА-ЯЁA-Z-]+$/.test(p));
  if (!nameLike) return full;
  return `${parts[0]} ${parts
    .slice(1)
    .map((p) => `${p[0]}.`)
    .join(' ')}`;
}

/**
 * langGenitive — родительный падеж названия языка для «Перевод с …»: русские
 * имена языков в основном прилагательные на «-ий» («Французский» →
 * «французского»). Не-прилагательные (Иврит, Хинди, Эсперанто) → null,
 * caller берёт запасную формулировку.
 */
export function langGenitive(name: string): string | null {
  const lower = name.trim().toLowerCase();
  if (lower.endsWith('ий')) return `${lower.slice(0, -2)}ого`;
  return null;
}

/**
 * translationLine — тихая строка «титульного листа» на карточке книги:
 * язык оригинала + переводчик одной естественной фразой
 * («Перевод с французского — Гинзбург Ю. А.»). null — не перевод/неизвестно.
 */
export function translationLine(
  srcLangName?: string | null,
  translator?: string | null,
): string | null {
  const gen = srcLangName ? langGenitive(srcLangName) : null;
  const person = translator ? shortPersonName(translator) : null;
  if (gen && person) return `Перевод с ${gen} — ${person}`;
  if (gen) return `Перевод с ${gen}`;
  // Название языка не склоняется («Иврит»/«Хинди») — запасная формулировка.
  if (srcLangName && person) return `Перевод — ${person} (оригинал: ${srcLangName.toLowerCase()})`;
  if (srcLangName) return `Оригинал: ${srcLangName.toLowerCase()}`;
  if (person) return `Перевод — ${person}`;
  return null;
}
