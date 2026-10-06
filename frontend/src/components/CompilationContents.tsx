import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { ListOrdered, Layers } from 'lucide-react';
import type { CompilationRef, ContentEntry } from '@/lib/books';

/** Сколько строк «Состава» видно до «Показать все». */
const CONTENTS_PREVIEW = 15;

const COMPILATION_KIND_LABEL: Record<CompilationRef['kind'], string> = {
  collection: 'сборник',
  anthology: 'антология',
  omnibus: 'том собрания',
};

/**
 * ContentsSection — «Состав» сборника из оглавления fb2 (#388). Строки, совпавшие
 * с отдельной книгой каталога, — ссылками на неё; остальные — просто текст
 * (рассказа нет в каталоге отдельно). Длинный состав свёрнут до первых строк.
 */
export function ContentsSection({ entries }: { entries: ContentEntry[] }) {
  const [all, setAll] = useState(false);
  if (entries.length === 0) return null;
  const shown = all ? entries : entries.slice(0, CONTENTS_PREVIEW);
  const hidden = entries.length - shown.length;
  return (
    <section className="space-y-2" aria-labelledby="contents-title">
      <h3 id="contents-title" className="flex items-center gap-2 text-sm font-medium">
        <ListOrdered className="size-4" aria-hidden />
        Состав
        <span className="text-xs font-normal text-muted-foreground tabular-nums">{entries.length}</span>
      </h3>
      <ol className="space-y-1 text-sm">
        {shown.map((e, i) => (
          <li key={i} className="flex gap-2">
            <span className="w-6 shrink-0 text-right text-xs leading-5 text-muted-foreground tabular-nums">{i + 1}.</span>
            {e.work_id ? (
              <Link
                to="/works/$id"
                params={{ id: String(e.work_id) }}
                className="text-pretty underline-offset-2 hover:underline"
              >
                {e.title}
              </Link>
            ) : (
              <span className="text-pretty text-muted-foreground">{e.title}</span>
            )}
          </li>
        ))}
      </ol>
      {hidden > 0 ? (
        <button
          type="button"
          onClick={() => setAll(true)}
          className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
        >
          Показать все ({entries.length})
        </button>
      ) : null}
    </section>
  );
}

/**
 * InCompilationsSection — «Входит в сборники»: сборники, в оглавлении которых
 * есть это произведение.
 */
export function InCompilationsSection({ items }: { items: CompilationRef[] }) {
  if (items.length === 0) return null;
  return (
    <section className="space-y-2" aria-labelledby="in-compilations-title">
      <h3 id="in-compilations-title" className="flex items-center gap-2 text-sm font-medium">
        <Layers className="size-4" aria-hidden />
        Входит в сборники
      </h3>
      <ul className="space-y-1 text-sm">
        {items.map((c) => (
          <li key={c.work_id} className="text-pretty">
            <Link to="/works/$id" params={{ id: String(c.work_id) }} className="underline-offset-2 hover:underline">
              {c.title}
            </Link>
            <span className="text-muted-foreground">
              {' · '}
              {COMPILATION_KIND_LABEL[c.kind] ?? 'сборник'}
              {c.author ? ` · ${c.author}` : ''}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
