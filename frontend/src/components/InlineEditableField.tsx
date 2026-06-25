import { useRef, useState, type ReactNode } from 'react';
import { Pencil } from 'lucide-react';
import { Input } from '@/components/ui/input';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useMe } from '@/lib/auth';
import { useSetOverride, useRevertOverride } from '@/lib/admin';
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
  kind: 'text' | 'int';
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
  const [editing, setEditing] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [draft, setDraft] = useState('');
  const longPress = useLongPress(() => setMenuOpen(true));

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
        if (!Number.isFinite(n)) return;
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

  // ── Режим редактирования: in-place input ──
  if (editing) {
    const input = (
      <span className="inline-flex items-center gap-1">
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') save();
            if (e.key === 'Escape') setEditing(false);
          }}
          onBlur={() => setEditing(false)}
          type={kind === 'int' ? 'number' : 'text'}
          className={cn(
            'h-7 px-1.5 py-0 text-sm',
            layout !== 'heading' && 'w-36 text-xs',
            layout === 'heading' && kind === 'int' && 'w-24',
            layout === 'heading' && kind === 'text' && 'w-full max-w-md',
          )}
          aria-label={label}
          autoFocus
        />
      </span>
    );
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

  const editor = (
    <span className="group/edit relative inline-flex items-center gap-1" {...longPress}>
      {valueNode}
      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger asChild>
          {/* opacity-0 (не display:none) — чтобы у меню был якорь и на тач. */}
          <button
            type="button"
            aria-label={`Изменить: ${label}`}
            className={cn(
              'shrink-0 rounded p-0.5 text-muted-foreground opacity-0 transition-opacity hover:text-foreground focus:opacity-100 group-hover/edit:opacity-100',
              overridden && 'opacity-60', // оверрайднутое — лёгкий намёк всегда
            )}
          >
            <Pencil className="size-3.5" aria-hidden />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-40">
          <DropdownMenuItem onSelect={startEdit}>Редактировать «{label}»</DropdownMenuItem>
          {overridden ? (
            <DropdownMenuItem
              onSelect={() => revert.mutate({ target_kind: targetKind, target_id: targetID, field })}
            >
              Отменить правку
            </DropdownMenuItem>
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
