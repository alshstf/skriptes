import { AlertTriangle } from 'lucide-react';
import { Callout } from '@/components/ui/callout';
import { useLibraryHealth } from '@/lib/admin';

/**
 * LibraryHealthCallout — плашка в админке, когда бэкенд не видит или не может
 * прочитать архивы книг из каталога (#632): поиск и карточки работают, а скачивание и ридер этих книг
 * отвечают «файл недоступен». Пропало всё — почти наверняка не подключён том
 * с книгами (на хосте диск смонтировался позже docker); часть — архивы удалены
 * с диска. Пока всё на месте — ничего не рисует.
 */
export function LibraryHealthCallout() {
  const { data } = useLibraryHealth();
  if (!data?.checked) return null;
  if (data.readable === false && data.missing < data.archives) {
    // Архивы на месте, но не читаются — пропало хранилище под томом (диск ВМ на
    // iSCSI-датасторе выключенного NAS, 2026-11-04). Вернётся само.
    return (
      <Callout icon={<AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
        <p className="font-medium text-foreground">Хранилище с книгами не отвечает</p>
        <p className="mt-1">
          Сервер не может прочитать архивы: скачивание и ридер сейчас отвечают «недоступно», поиск и карточки —
          работают. Проверьте хранилище, на котором лежит том с книгами (SKRIPTES_BOOKS_ROOT); когда оно вернётся, всё
          заработает само, в течение минуты.
        </p>
        {data.read_error && <p className="mt-1 break-all">Ошибка: {data.read_error}</p>}
      </Callout>
    );
  }
  if (data.missing === 0) return null;
  const all = data.missing === data.archives;
  return (
    <Callout icon={<AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
      <p className="font-medium text-foreground">
        {all
          ? 'Сервер не видит ни одного архива с книгами'
          : `Сервер не видит ${data.missing} из ${data.archives} архивов с книгами`}
      </p>
      <p className="mt-1">
        Скачивание и ридер для их книг не работают, поиск и карточки — работают.{' '}
        {all
          ? 'Скорее всего, не подключён том с книгами (SKRIPTES_BOOKS_ROOT): проверьте диск на хосте и перезапустите backend.'
          : 'Проверьте, что архивы лежат в SKRIPTES_BOOKS_ROOT.'}
      </p>
      {!all && data.sample.length > 0 && (
        <p className="mt-1 break-all">
          Например: {data.sample.join(', ')}
          {data.missing > data.sample.length ? '…' : ''}
        </p>
      )}
    </Callout>
  );
}
