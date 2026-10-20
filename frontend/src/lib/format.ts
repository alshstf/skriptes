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

/**
 * pluralRu — русское склонение числительного: forms = [одна, две–четыре, пять]
 * («книга», «книги», «книг»); 11–14 — как «пять».
 */
export function pluralRu(n: number, forms: readonly [string, string, string]): string {
  const mod10 = Math.abs(n) % 10;
  const mod100 = Math.abs(n) % 100;
  if (mod100 >= 11 && mod100 <= 14) return forms[2];
  if (mod10 === 1) return forms[0];
  if (mod10 >= 2 && mod10 <= 4) return forms[1];
  return forms[2];
}

/** pluralBooks — «книга / книги / книг». */
export function pluralBooks(n: number): string {
  return pluralRu(n, ['книга', 'книги', 'книг']);
}

/**
 * formatRelativeDays — «сегодня» / «вчера» / «3 дня назад» / «2 недели назад» /
 * «4 месяца назад» / «2 года назад» по календарным дням (локальная полночь).
 * Будущее (рассинхрон часов) — «сегодня».
 */
export function formatRelativeDays(iso: string, now = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.floor((startOf(now) - startOf(d)) / 86_400_000);
  if (days <= 0) return 'сегодня';
  if (days === 1) return 'вчера';
  if (days < 7) return `${days} ${pluralRu(days, ['день', 'дня', 'дней'])} назад`;
  if (days < 30) {
    const w = Math.floor(days / 7);
    return `${w} ${pluralRu(w, ['неделю', 'недели', 'недель'])} назад`;
  }
  if (days < 365) {
    const m = Math.floor(days / 30);
    return `${m} ${pluralRu(m, ['месяц', 'месяца', 'месяцев'])} назад`;
  }
  const y = Math.floor(days / 365);
  return `${y} ${pluralRu(y, ['год', 'года', 'лет'])} назад`;
}

/**
 * formatReadingTime — «≈ 5 мин» / «≈ 1 ч 20 мин» / «≈ 4 ч»: до часа — с шагом
 * 5 минут, больше — часы и минуты с шагом 10 (точность оценки скорости чтения
 * не выше). 0 и меньше — пустая строка.
 */
export function formatReadingTime(minutes: number): string {
  if (!Number.isFinite(minutes) || minutes <= 0) return '';
  if (minutes < 60) {
    const m = Math.max(5, Math.round(minutes / 5) * 5);
    return `≈ ${m} мин`;
  }
  const total = Math.round(minutes / 10) * 10;
  const h = Math.floor(total / 60);
  const m = total % 60;
  return m === 0 ? `≈ ${h} ч` : `≈ ${h} ч ${m} мин`;
}
