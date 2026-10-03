import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Skeleton } from '@/components/ui/skeleton';
import { AdminTabs } from '@/components/AdminTabs';
import { ContentEditor } from '@/components/ContentVisibility';
import { SaveBar } from '@/components/SaveBar';
import { useAdminContent, useUpdateAdminContent, useLanguages, sameSet, type ContentSettings } from '@/lib/content';
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
  // Режим «только выбранные языки» (#310): храним показываемые; новые языки
  // следующих INPX скрыты сами.
  const [onlyMode, setOnlyMode] = useState(false);
  const [shownLangs, setShownLangs] = useState<string[]>([]);

  const reset = useCallback((d: ContentSettings) => {
    setHiddenGenres(d.hidden_genres);
    setHiddenLangs(d.hidden_languages);
    setOnlyMode(d.language_mode === 'only');
    setShownLangs(d.shown_languages ?? []);
  }, []);

  useEffect(() => {
    if (content.data) reset(content.data);
  }, [content.data, reset]);

  const allLangs = (langsQ.data ?? []).map((l) => l.code);
  const dirty = content.data
    ? !sameSet(hiddenGenres, content.data.hidden_genres) ||
      onlyMode !== (content.data.language_mode === 'only') ||
      (onlyMode
        ? !sameSet(shownLangs, content.data.shown_languages ?? [])
        : !sameSet(hiddenLangs, content.data.hidden_languages))
    : false;

  // Переключение режима сохраняет то, что сейчас видно: «только выбранные» =
  // видимые сейчас языки, и наоборот.
  const switchMode = (only: boolean) => {
    if (only === onlyMode) return;
    if (only) setShownLangs(allLangs.filter((c) => !hiddenLangs.includes(c)));
    else setHiddenLangs(allLangs.filter((c) => !shownLangs.includes(c)));
    setOnlyMode(only);
  };

  const onSave = async () => {
    try {
      await update.mutateAsync({
        hidden_genres: hiddenGenres,
        hidden_languages: onlyMode ? [] : hiddenLangs,
        language_mode: onlyMode ? 'only' : '',
        shown_languages: onlyMode ? shownLangs : [],
      });
      toast.success('Настройки контента сохранены');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  const onReset = () => {
    if (content.data) reset(content.data);
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
          hiddenLanguages={onlyMode ? allLangs.filter((c) => !shownLangs.includes(c)) : hiddenLangs}
          onChangeGenres={setHiddenGenres}
          onChangeLanguages={(next) =>
            onlyMode ? setShownLangs(allLangs.filter((c) => !next.includes(c))) : setHiddenLangs(next)
          }
          languageModeControl={
            <div className="space-y-1.5">
              <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm">Новые языки</span>
              <div className="inline-flex rounded-md border border-border p-0.5" role="group" aria-label="Новые языки">
                {[
                  { only: false, label: 'Показывать' },
                  { only: true, label: 'Скрывать' },
                ].map((m) => (
                  <button
                    key={String(m.only)}
                    type="button"
                    aria-pressed={onlyMode === m.only}
                    onClick={() => switchMode(m.only)}
                    className={
                      onlyMode === m.only
                        ? 'rounded bg-muted px-2.5 py-1 text-xs font-medium'
                        : 'rounded px-2.5 py-1 text-xs text-muted-foreground hover:text-foreground'
                    }
                  >
                    {m.label}
                  </button>
                ))}
              </div>
              </div>
              <p className="text-xs text-muted-foreground text-pretty">
                {onlyMode
                  ? 'Видны только неотмеченные языки. Язык, который впервые появится с будущим INPX, будет скрыт сам.'
                  : 'Язык, который впервые появится с будущим INPX, будет виден, пока его не отметить.'}
              </p>
            </div>
          }
          footer={
            dirty ? (
              <SaveBar saving={update.isPending} onSave={onSave} onReset={onReset} />
            ) : null
          }
        />
      )}
    </article>
  );
}
