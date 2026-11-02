import type { MouseEvent } from 'react';
import { toast } from 'sonner';
import { ApiError, apiFetch } from '@/lib/api';

// downloadUrl — ссылка скачивания книги в формате (href пунктов меню «Скачать»).
export function downloadUrl(bookId: number, format: string): string {
  return `/api/books/${bookId}/download?format=${encodeURIComponent(format)}`;
}

// downloadErrorMessage — текст тоста: сервер кладёт его в поле message (#631).
export function downloadErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.isNetworkError()) return err.message;
    if (err.isUnauthorized()) return 'Сессия истекла — войдите снова.';
    const body = err.body;
    if (body && typeof body === 'object' && 'message' in body && typeof body.message === 'string') {
      return body.message;
    }
  }
  return 'Не удалось скачать книгу.';
}

/**
 * downloadBook — скачать книгу, сначала спросив сервер, на месте ли файл.
 *
 * Ссылка с атрибутом download сохраняет файлом любой ответ: ошибку читатель
 * получал как download.json с {"error": …} (#631, том с книгами не был
 * подключён). Поэтому сначала дешёвая проверка (без конвертации), и только
 * если всё в порядке — та же ссылка программным кликом. Ошибка — тостом.
 */
export async function downloadBook(bookId: number, format: string): Promise<void> {
  try {
    await apiFetch<void>(`/api/books/${bookId}/download/check?format=${encodeURIComponent(format)}`);
  } catch (err) {
    toast.error(downloadErrorMessage(err));
    return;
  }
  const a = document.createElement('a');
  a.href = downloadUrl(bookId, format);
  a.download = '';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/**
 * onDownloadClick — onClick пункта меню «Скачать»: обычный клик идёт через
 * downloadBook. Клик с модификатором (новая вкладка, «Загрузить связанный
 * файл») — по href, как раньше: такой ссылке и оставлены href и download.
 *
 * close — закрыть меню: Radix не закрывает пункт, чей клик отменён
 * (preventDefault), поэтому меню у вызывающего — управляемое.
 */
export function onDownloadClick(bookId: number, format: string, close: () => void) {
  return (e: MouseEvent<HTMLAnchorElement>) => {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    close();
    void downloadBook(bookId, format);
  };
}
