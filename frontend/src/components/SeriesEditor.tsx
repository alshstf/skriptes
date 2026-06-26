import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { Pencil, RotateCcw, X } from 'lucide-react';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Command, CommandInput, CommandItem, CommandList } from '@/components/ui/command';
import { useMe } from '@/lib/auth';
import { useSuggest } from '@/lib/suggest';
import { useSetOverride, useRevertOverride } from '@/lib/admin';
import { useLongPress } from '@/lib/useLongPress';

type SeriesRef = { id: number; title: string };

/**
 * SeriesEditor — серия работы с переносом для админа (оверрайд series, грабля №19).
 * Не-админ: «Серия: ссылка» (как было). Админ: + правка (десктоп ховер-карандаш /
 * мобила лонг-тап) → поповер: поиск по СУЩЕСТВУЮЩИМ сериям (useSuggest) + «убрать из
 * серии». Перенос материализует series_id на works + все издания (номер #N сохраняется —
 * его правит отдельный ser_no-редактор). Создание новой серии — отдельный follow-up.
 */
export function SeriesEditor({
  workId,
  series,
  serNo,
  overridden = false,
}: {
  workId: number;
  series: SeriesRef | null;
  serNo: number | null;
  overridden?: boolean;
}) {
  const me = useMe();
  const label = (
    <>
      <span className="text-muted-foreground">Серия:</span>{' '}
      {series ? (
        <Link to="/series/$id" params={{ id: String(series.id) }} className="hover:underline">
          {series.title}
        </Link>
      ) : (
        <span className="italic text-muted-foreground">нет</span>
      )}
    </>
  );
  if (me.data?.role !== 'admin') return series ? <span>{label}</span> : null;
  return <AdminSeries workId={workId} series={series} serNo={serNo} overridden={overridden} label={label} />;
}

function AdminSeries({
  workId,
  series,
  serNo,
  overridden,
  label,
}: {
  workId: number;
  series: SeriesRef | null;
  serNo: number | null;
  overridden: boolean;
  label: React.ReactNode;
}) {
  const setOverride = useSetOverride();
  const revert = useRevertOverride();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const longPress = useLongPress(() => {
    setSearch('');
    setOpen(true);
  });
  const suggest = useSuggest(open ? search : '', 8);
  const candidates = (suggest.data?.series ?? []).filter((s) => s.id !== series?.id);

  // Перенос: series_id меняется, номер #N сохраняем (его правит ser_no-редактор).
  function move(seriesID: number | null) {
    setOverride.mutate(
      {
        target_kind: 'work',
        target_id: workId,
        field: 'series',
        value: { series_id: seriesID, ser_no: seriesID === null ? null : serNo },
      },
      { onSuccess: () => setOpen(false) },
    );
  }

  return (
    <span className="group/ser relative inline-flex items-center gap-1" {...longPress}>
      {label}
      <Popover
        open={open}
        onOpenChange={(o) => {
          if (o) setSearch('');
          setOpen(o);
        }}
      >
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label="Перенести в другую серию"
            className="shrink-0 rounded p-0.5 text-muted-foreground opacity-0 transition-opacity hover:text-foreground focus:opacity-100 group-hover/ser:opacity-100"
          >
            <Pencil className="size-3.5" aria-hidden />
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-72 p-0">
          <Command shouldFilter={false}>
            <CommandInput value={search} onValueChange={setSearch} placeholder="Найти серию…" />
            <CommandList>
              {series ? (
                <CommandItem value="__remove" onSelect={() => move(null)}>
                  <X className="mr-2 size-4 shrink-0 text-muted-foreground" aria-hidden />
                  <span className="text-muted-foreground">Убрать из серии</span>
                </CommandItem>
              ) : null}
              {search.trim().length < 2 ? (
                <div className="py-4 text-center text-xs text-muted-foreground">Введите ≥2 символов</div>
              ) : candidates.length > 0 ? (
                candidates.map((s) => (
                  <CommandItem key={s.id} value={String(s.id)} onSelect={() => move(s.id)}>
                    <span className="flex-1 truncate">{s.title}</span>
                    {s.author_name ? (
                      <span className="ml-2 shrink-0 text-xs text-muted-foreground">{s.author_name}</span>
                    ) : null}
                  </CommandItem>
                ))
              ) : (
                <div className="py-4 text-center text-xs text-muted-foreground">
                  {suggest.isFetching ? 'Поиск…' : 'Не найдено'}
                </div>
              )}
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      {overridden ? (
        <button
          type="button"
          onClick={() => revert.mutate({ target_kind: 'work', target_id: workId, field: 'series' })}
          disabled={revert.isPending}
          aria-label="Отменить перенос серии"
          className="shrink-0 rounded p-0.5 text-muted-foreground opacity-60 transition-opacity hover:text-foreground focus:opacity-100 group-hover/ser:opacity-100 disabled:opacity-30"
        >
          <RotateCcw className="size-3" aria-hidden />
        </button>
      ) : null}
    </span>
  );
}
