import { useCanGoBack, useRouter } from '@tanstack/react-router';
import { ArrowLeft } from 'lucide-react';
import { Button } from '@/components/ui/button';

/**
 * BackButton — универсальная кнопка "Назад" для любой страницы внутри
 * каталога. Поведение:
 *   - если в browser history есть запись, на которую можно вернуться
 *     (useCanGoBack) — делаем history.back(): пользователь возвращается
 *     ровно туда, откуда пришёл (карточка автора → книга → назад →
 *     карточка автора)
 *   - иначе — фолбэк на /books (например, при заходе на /books/$id по
 *     прямой ссылке, когда back истории нет)
 *
 * Раньше эти кнопки были захардкожены как Link to="/books" — это давало
 * скачкообразную "потерю места": из любой глубины пользователя
 * выкидывало на верхний список вместо логичного шага назад.
 */
export function BackButton({ fallbackTo = '/books', label = 'Назад' }: { fallbackTo?: string; label?: string }) {
  const router = useRouter();
  const canGoBack = useCanGoBack();
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => {
        if (canGoBack) {
          router.history.back();
        } else {
          // navigate ожидает строго typed `to`; fallbackTo приходит как
          // строка (наши страницы используют '/books'), поэтому unknown-cast.
          router.navigate({ to: fallbackTo } as unknown as Parameters<typeof router.navigate>[0]);
        }
      }}
    >
      <ArrowLeft className="mr-2 size-4" aria-hidden />
      {label}
    </Button>
  );
}
