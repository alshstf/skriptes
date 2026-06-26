import { useRef, useState, type ReactNode } from 'react';
import { Pencil, RotateCcw } from 'lucide-react';
import { Input } from '@/components/ui/input';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useMe } from '@/lib/auth';
import { useSetOverride, useRevertOverride } from '@/lib/admin';
import { useLanguages } from '@/lib/content';
import { cn } from '@/lib/utils';

/**
 * InlineEditableField — поле с правкой для админа (локальные оверрайды каталога,
 * грабля №19). По умолчанию ВИЗУАЛЬНО НЕЗАМЕТНО (как обычный текст) — правят редко,
 * карточку в основном читают. Affordance:
 *   - десктоп: при ховере у значения появляется карандаш → меню (in-place, без
 *     отдельной панели);
 *   - мобила: лонг-тап по значению → то же action-меню.
 * Меню: «Редактировать» (→ in-place input) + «Отменить правку» (если оверрайднуто).
 * Не-админ видит обычную мету (скрыта если пусто).
 *
 * layout: 'inline' — «метка: значение» (EditionRow); 'grid' — <dt>/<dd> (FileDetails);
 *         'heading' — крупный текст (заголовок карточки), правка тоже на месте.
 */
type Props = {
  targetKind: 'book' | 'work';
  targetID: number;
  field: string;
  value: string | number | null | undefined;
  kind: 'text' | 'int' | 'lang';
  label: string;
  overridden?: boolean;
  mono?: boolean;
  layout?: 'inline' | 'grid' | 'heading';
  /** Для layout='heading' — отрисовка значения родителем (CardTitle и т.п.). */
  children?: ReactNode;
};

export function InlineEditableField(props: Props) {
  const { value, label, mono = false, layout = 'inline', children } = props;
  const me = useMe();
  const isAdmin = me.data?.role === 'admin';
  const display = value === null || value === undefined || value === '' ? null : String(value);

  if (!isAdmin) {
    return <PlainValue layout={layout} label={label} display={display} mono={mono}>{children}</PlainValue>;
  }
  return <AdminEditable {...props} display={display} />;
}

// PlainValue — отрисовка значения без правки (не-админ + не редактируем).
function PlainValue({
  layout,
  label,
  display,
  mono,
  children,
}: {
  layout: 'inline' | 'grid' | 'heading';
  label: string;
  display: string | null;
  mono: boolean;
  children?: ReactNode;
}) {
  if (layout === 'heading') return <>{children}</>;
  if (!display) return null;
  if (layout === 'grid') {
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

// useLongPress — лонг-тап (тач) для вызова меню правки. preventDefault на
// contextmenu подавляет нативное long-press-меню браузера.
function useLongPress(onLongPress: () => void, ms = 450) {
  const timer = useRef<number | null>(null);
  const clear = () => {
    if (timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
    }
  };
  return {
    onTouchStart: () => {
      clear();
      timer.current = window.setTimeout(onLongPress, ms);
    },
    onTouchEnd: clear,
    onTouchMove: clear,
    onTouchCancel: clear,
    onContextMenu: (e: React.MouseEvent) => e.preventDefault(),
  };
}

function AdminEditable({
  targetKind,
  targetID,
  field,
  kind,
  label,
  overridden = false,
  mono = false,
  layout = 'inline',
  display,
  children,
}: Props & { display: string | null }) {
  const setOverride = useSetOverride();
  const revert = useRevertOverride();
  const langs = useLanguages();
  const [editing, setEditing] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [draft, setDraft] = useState('');
  const longPress = useLongPress(() => setMenuOpen(true));

  function startEdit() {
    setDraft(display ?? '');
    setEditing(true);
  }
  function commit(v: string | number | null) {
    setOverride.mutate(
      { target_kind: targetKind, target_id: targetID, field, value: { v } },
      { onSuccess: () => setEditing(false) },
    );
  }

  function save() {
    const raw = draft.trim();
    let v: string | number | null = null;
    if (raw !== '') {
      if (kind === 'int') {
        const n = Number(raw);
        if (!Number.isFinite(n)) return;
        v = n;
      } else {
        v = raw;
      }
    }
    commit(v);
  }

  // ── Режим редактирования: in-place input/select ──
  if (editing) {
    // lang — выбор из языков коллекции (текущий код — display — добавляем, если его нет).
    const langOpts: { code: string; display: string }[] = (langs.data ?? []).map((i) => ({
      code: i.code,
      display: i.display,
    }));
    if (display && !langOpts.some((o) => o.code === display)) {
      langOpts.unshift({ code: display, display });
    }
    const editControl =
      kind === 'lang' ? (
        <select
          value={display ?? ''}
          onChange={(e) => commit(e.target.value || null)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') setEditing(false);
          }}
          onBlur={() => setEditing(false)}
          // text-base (16px) на мобиле — иначе iOS Safari зумит поле на фокусе.
          className="h-8 rounded-md border border-input bg-transparent px-2 text-base focus:outline-none focus:ring-1 focus:ring-ring md:text-sm"
          aria-label={label}
          autoFocus
        >
          {langOpts.map((o) => (
            <option key={o.code} value={o.code}>
              {o.display}
            </option>
          ))}
        </select>
      ) : (
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') save();
            if (e.key === 'Escape') setEditing(false);
          }}
          onBlur={() => setEditing(false)}
          type={kind === 'int' ? 'number' : 'text'}
          // Размер шрифта НЕ переопределяем: базовый Input = text-base (16px) на
          // мобиле + md:text-sm на десктопе. <16px заставил бы iOS Safari зумить
          // поле на фокусе (и зум «залипал»).
          className={cn(
            'h-8 px-1.5 py-0',
            layout !== 'heading' && 'w-36',
            layout === 'heading' && kind === 'int' && 'w-24',
            layout === 'heading' && kind === 'text' && 'w-full max-w-md',
          )}
          aria-label={label}
          autoFocus
        />
      );
    const input = <span className="inline-flex items-center gap-1">{editControl}</span>;
    if (layout === 'grid') {
      return (
        <>
          <dt className="text-muted-foreground">{label}</dt>
          <dd>{input}</dd>
        </>
      );
    }
    if (layout === 'heading') return input;
    return (
      <span className="inline-flex items-center gap-1">
        <span className="opacity-70">{label}:</span> {input}
      </span>
    );
  }

  // ── Просмотр: значение + (ховер) карандаш-триггер меню + лонг-тап ──
  const valueNode =
    layout === 'heading' ? (
      children
    ) : (
      <span className={cn(mono && 'font-mono break-all', display ? 'text-foreground/80' : 'italic text-muted-foreground')}>
        {display ?? '—'}
      </span>
    );

  const doRevert = () => revert.mutate({ target_kind: targetKind, target_id: targetID, field });

  const editor = (
    <span className="group/edit relative inline-flex items-center gap-1" {...longPress}>
      {valueNode}
      {/* Десктоп: карандаш на ховере → СРАЗУ in-place правка (+ ↺ откат, если
          оверрайднуто). Иконки opacity-0/group-hover; на тач не видны. */}
      <button
        type="button"
        onClick={startEdit}
        aria-label={`Изменить: ${label}`}
        className="shrink-0 rounded p-0.5 text-muted-foreground opacity-0 transition-opacity hover:text-foreground focus:opacity-100 group-hover/edit:opacity-100"
      >
        <Pencil className="size-3.5" aria-hidden />
      </button>
      {overridden ? (
        <button
          type="button"
          onClick={doRevert}
          disabled={revert.isPending}
          aria-label="Отменить правку"
          className="shrink-0 rounded p-0.5 text-muted-foreground opacity-60 transition-opacity hover:text-foreground focus:opacity-100 group-hover/edit:opacity-100 disabled:opacity-30"
        >
          <RotateCcw className="size-3" aria-hidden />
        </button>
      ) : null}
      {/* Мобила: лонг-тап → action-меню. Триггер — невидимый якорь поверх значения
          (pointer-events-none, не перехватывает обычный тап), открывается через
          menuOpen из useLongPress. */}
      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger asChild>
          <span className="pointer-events-none absolute inset-0" aria-hidden />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-40">
          <DropdownMenuItem onSelect={startEdit}>Редактировать «{label}»</DropdownMenuItem>
          {overridden ? (
            <DropdownMenuItem onSelect={doRevert}>Отменить правку</DropdownMenuItem>
          ) : null}
        </DropdownMenuContent>
      </DropdownMenu>
    </span>
  );

  if (layout === 'heading') return editor;
  if (layout === 'grid') {
    return (
      <>
        <dt className="text-muted-foreground">{label}</dt>
        <dd>{editor}</dd>
      </>
    );
  }
  return (
    <span className="inline-flex items-center gap-1">
      <span className="opacity-70">{label}:</span> {editor}
    </span>
  );
}
