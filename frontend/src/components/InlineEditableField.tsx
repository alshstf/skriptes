import { useState } from 'react';
import { Check, Pencil, RotateCcw, X } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { useMe } from '@/lib/auth';
import { useSetOverride, useRevertOverride } from '@/lib/admin';
import { cn } from '@/lib/utils';

/**
 * InlineEditableField — мета-поле «метка: значение» с inline-правкой для админа
 * (локальные оверрайды каталога). Не-админ видит обычную мету (скрыта если пусто),
 * админ — карандаш → input → сохранить; если поле уже оверрайднуто, бейдж
 * «изменено» + откат (↺). Значение материализуется на бэке в реальную колонку.
 *
 * layout 'inline' — строка «метка: значение» (секция «Издания», EditionRow);
 *        'grid'   — пара <dt>/<dd> в grid-`<dl>` (раскрывашка «Детали файла»).
 *
 * PR1 — скалярные edition-поля (kind 'text'|'int'). Серия/жанры/авторы — позже.
 */
type Props = {
  targetKind: 'book' | 'work';
  targetID: number;
  field: string;
  value: string | number | null | undefined;
  kind: 'text' | 'int';
  label: string;
  overridden?: boolean;
  mono?: boolean;
  layout?: 'inline' | 'grid';
};

export function InlineEditableField({
  targetKind,
  targetID,
  field,
  value,
  kind,
  label,
  overridden = false,
  mono = false,
  layout = 'inline',
}: Props) {
  const me = useMe();
  const isAdmin = me.data?.role === 'admin';
  const setOverride = useSetOverride();
  const revert = useRevertOverride();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');

  const display = value === null || value === undefined || value === '' ? null : String(value);
  const isGrid = layout === 'grid';

  // Не-админ: обычная мета, скрыта если значения нет.
  if (!isAdmin) {
    if (!display) return null;
    if (isGrid) {
      return (
        <>
          <dt className="text-muted-foreground">{label}</dt>
          <dd className={cn(mono && 'font-mono text-xs break-all')}>{display}</dd>
        </>
      );
    }
    return (
      <span>
        <span className="opacity-70">{label}:</span>{' '}
        <span className={cn('text-foreground/80', mono && 'font-mono break-all')}>{display}</span>
      </span>
    );
  }

  function startEdit() {
    setDraft(display ?? '');
    setEditing(true);
  }

  function save() {
    const raw = draft.trim();
    let v: string | number | null = null;
    if (raw !== '') {
      if (kind === 'int') {
        const n = Number(raw);
        if (!Number.isFinite(n)) return; // невалидное число — игнор
        v = n;
      } else {
        v = raw;
      }
    }
    setOverride.mutate(
      { target_kind: targetKind, target_id: targetID, field, value: { v } },
      { onSuccess: () => setEditing(false) },
    );
  }

  // controls — значение/инпут + кнопки (БЕЗ метки; метку кладёт обёртка по layout).
  const controls = editing ? (
    <span className="inline-flex items-center gap-1">
      <Input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') save();
          if (e.key === 'Escape') setEditing(false);
        }}
        type={kind === 'int' ? 'number' : 'text'}
        className="h-6 w-32 px-1 py-0 text-xs"
        aria-label={label}
        autoFocus
      />
      <button
        type="button"
        onClick={save}
        disabled={setOverride.isPending}
        aria-label="Сохранить"
        className="text-foreground/70 hover:text-foreground disabled:opacity-50"
      >
        <Check className="size-3.5" aria-hidden />
      </button>
      <button
        type="button"
        onClick={() => setEditing(false)}
        aria-label="Отмена"
        className="text-muted-foreground hover:text-foreground"
      >
        <X className="size-3.5" aria-hidden />
      </button>
    </span>
  ) : (
    <span className="group/edit inline-flex items-center gap-1">
      <button
        type="button"
        onClick={startEdit}
        className={cn(
          'text-left hover:underline',
          mono && 'font-mono break-all',
          display ? 'text-foreground/80' : 'italic text-muted-foreground',
        )}
      >
        {display ?? 'добавить'}
      </button>
      {overridden ? (
        <span className="inline-flex items-center gap-0.5">
          <span className="rounded bg-muted px-1 text-[10px] text-muted-foreground">изменено</span>
          <button
            type="button"
            onClick={() => revert.mutate({ target_kind: targetKind, target_id: targetID, field })}
            disabled={revert.isPending}
            aria-label="Отменить правку"
            className="text-muted-foreground hover:text-foreground disabled:opacity-50"
          >
            <RotateCcw className="size-3" aria-hidden />
          </button>
        </span>
      ) : (
        <Pencil
          className="size-3 text-muted-foreground opacity-0 transition-opacity group-hover/edit:opacity-100"
          aria-hidden
        />
      )}
    </span>
  );

  if (isGrid) {
    return (
      <>
        <dt className="text-muted-foreground">{label}</dt>
        <dd>{controls}</dd>
      </>
    );
  }
  return (
    <span className="inline-flex items-center gap-1">
      <span className="opacity-70">{label}:</span> {controls}
    </span>
  );
}
