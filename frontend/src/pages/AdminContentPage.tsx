import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Skeleton } from '@/components/ui/skeleton';
import { AdminTabs } from '@/components/AdminTabs';
import { ContentEditor, ContentSaveBar } from '@/components/ContentVisibility';
import { useAdminContent, useUpdateAdminContent, useLanguages, sameSet } from '@/lib/content';
import { useGenres } from '@/lib/genres';
import { ApiError } from '@/lib/api';

/**
 * AdminContentPage — /admin/content. Глобальная видимость контента: какие
 * языки и жанры скрыты для ВСЕХ пользователей сервера. Скрытое здесь не
 * показывается нигде (список/поиск/фильтры) и недоступно по прямой ссылке
 * (404). Применяется сразу при сохранении, без рестарта.
 */
export function AdminContentPage() {
  const content = useAdminContent();
  const langsQ = useLanguages();
  const genresQ = useGenres();
  const update = useUpdateAdminContent();

  const [hiddenGenres, setHiddenGenres] = useState<string[]>([]);
  const [hiddenLangs, setHiddenLangs] = useState<string[]>([]);

  useEffect(() => {
    if (content.data) {
      setHiddenGenres(content.data.hidden_genres);
      setHiddenLangs(content.data.hidden_languages);
    }
  }, [content.data]);

  const dirty = content.data
    ? !sameSet(hiddenGenres, content.data.hidden_genres) ||
      !sameSet(hiddenLangs, content.data.hidden_languages)
    : false;

  const onSave = async () => {
    try {
      await update.mutateAsync({ hidden_genres: hiddenGenres, hidden_languages: hiddenLangs });
      toast.success('Настройки контента сохранены');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  const onReset = () => {
    if (content.data) {
      setHiddenGenres(content.data.hidden_genres);
      setHiddenLangs(content.data.hidden_languages);
    }
  };

  const loading = content.isLoading || langsQ.isLoading || genresQ.isLoading;
  const failed = content.error || langsQ.error || genresQ.error;

  return (
    <article className="space-y-6">
      <AdminTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Контент</h1>
        <p className="text-sm text-muted-foreground">
          Отметьте языки и жанры, которые нужно скрыть для всех пользователей сервера. Скрытые
          книги не показываются в списке, поиске и фильтрах и недоступны по прямой ссылке.
        </p>
      </header>

      {loading ? (
        <Skeleton className="h-64 w-full" />
      ) : failed ? (
        <p className="text-sm text-destructive">Не удалось загрузить данные.</p>
      ) : (
        <ContentEditor
          languages={langsQ.data ?? []}
          genres={genresQ.data ?? []}
          hiddenGenres={hiddenGenres}
          hiddenLanguages={hiddenLangs}
          onChangeGenres={setHiddenGenres}
          onChangeLanguages={setHiddenLangs}
          footer={
            dirty ? (
              <ContentSaveBar saving={update.isPending} onSave={onSave} onReset={onReset} />
            ) : null
          }
        />
      )}
    </article>
  );
}
