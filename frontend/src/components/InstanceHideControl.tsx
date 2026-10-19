import { EyeOff } from 'lucide-react';
import { Link } from '@tanstack/react-router';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { useMe } from '@/lib/auth';
import { useCanEdit } from '@/lib/editMode';
import { fetchHidePreview, useHiddenStatus, useSetHidden, type HideKind } from '@/lib/hiding';

/**
 * InstanceHideControl — скрытие автора или работы на инстансе (админ, 0064).
 * Плашка «Скрыто на инстансе» — админу всегда (сюда он попадает по ссылке, в
 * списках скрытого нет); кнопки «Скрыть»/«Вернуть» — в режиме правки (#444).
 * bookId (карточка работы) — открытое издание: работа может быть видна, а само
 * издание скрыто (антология со скрытым соавтором) — тогда плашка издания.
 */
export function InstanceHideControl({
  kind,
  id,
  name,
  bookId,
}: {
  kind: HideKind;
  id: number | undefined;
  name: string;
  bookId?: number;
}) {
  const { data: me } = useMe();
  const isAdmin = me?.role === 'admin';
  const canEdit = useCanEdit();
  const status = useHiddenStatus(kind, id, isAdmin);
  const bookStatus = useHiddenStatus('book', bookId, isAdmin && kind === 'work');
  const setHidden = useSetHidden();
  if (!isAdmin || !id || !status.data) return null;
  // Работа видна, а открытое издание скрыто — плашка издания (вернуть — автора).
  const st = !status.data.hidden && bookStatus.data?.hidden ? bookStatus.data : status.data;
  const ownHidden = status.data.hidden;

  if (st.hidden) {
    return (
      <Callout icon={<EyeOff className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
        <span className="font-medium text-foreground">Скрыто на инстансе.</span>{' '}
        Никто, кроме админа, не видит{' '}
        {kind === 'author' ? 'этого автора и его книги' : ownHidden ? 'эту книгу' : 'это издание'} — ни в каталоге
        и поиске, ни в OPDS, ни по ссылке; в избранном и на полках её нет.
        {st.via.length > 0 ? (
          <>
            {' '}
            Скрыто, потому что {st.via.length > 1 ? 'скрыты' : 'скрыт'}{' '}
            {st.via.map((r, i) => (
              <span key={`${r.kind}-${r.id}`}>
                {i > 0 ? ', ' : ''}
                {r.kind === 'author' ? 'автор ' : 'работа '}
                <Link
                  to={r.kind === 'author' ? '/authors/$id' : '/works/$id'}
                  params={{ id: String(r.id) }}
                  className="underline"
                >
                  {r.name}
                </Link>
              </span>
            ))}
            .
          </>
        ) : null}
        {ownHidden && st.explicit && canEdit ? (
          <Button
            variant="outline"
            size="sm"
            className="ml-2 h-6 px-2 text-xs"
            disabled={setHidden.isPending}
            onClick={() => setHidden.mutate({ kind, id, hide: false })}
          >
            Вернуть
          </Button>
        ) : null}
      </Callout>
    );
  }

  if (!canEdit) return null;

  const onHide = async () => {
    let msg = `Скрыть «${name}» на инстансе?`;
    try {
      const p = await fetchHidePreview(kind, id);
      msg +=
        kind === 'author'
          ? ` Скроются его издания — ${p.books} (работ — до ${p.works}), включая совместные и сборники с ним.`
          : ` Скроются её издания — ${p.books}.`;
    } catch {
      // без прогноза — подтверждение всё равно нужно
    }
    msg +=
      ' Никто, кроме админа, их не увидит — ни в каталоге и поиске, ни в OPDS, ни по ссылке; из избранного и' +
      ' с полок пропадут молча. Вернуть — здесь или в «Администрирование → Скрытое».';
    if (!window.confirm(msg)) return;
    setHidden.mutate({ kind, id, hide: true });
  };

  return (
    <div>
      <Button
        variant="ghost"
        size="sm"
        className="gap-1 text-muted-foreground"
        onClick={onHide}
        disabled={setHidden.isPending}
      >
        <EyeOff className="size-4" aria-hidden />
        {setHidden.isPending ? 'Скрываю…' : 'Скрыть на инстансе'}
      </Button>
    </div>
  );
}
