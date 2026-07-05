import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Layers } from 'lucide-react';
import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { ProfileTabs } from '@/components/ProfileTabs';
import { ContentEditor, HideRow } from '@/components/ContentVisibility';
import { SaveBar } from '@/components/SaveBar';
import { useMyContent, useUpdateMyContent, useLanguages, sameSet } from '@/lib/content';
import { useGenres } from '@/lib/genres';
import { ApiError } from '@/lib/api';

/**
 * ProfileContentPage — /me/content. Персональная видимость контента: какие
 * языки и жанры скрыть лично для себя (в списке/поиске/фильтрах). Не
 * влияет на других пользователей.
 *
 * Скрытое глобально администратором приходит как locked: показано
 * отмеченным с замком и недоступно для изменения — переопределить
 * глобальную настройку нельзя.
 */
export function ProfileContentPage() {
  const content = useMyContent();
  const langsQ = useLanguages();
  const genresQ = useGenres();
  const update = useUpdateMyContent();

  const [hiddenGenres, setHiddenGenres] = useState<string[]>([]);
  const [hiddenLangs, setHiddenLangs] = useState<string[]>([]);
  const [hideComps, setHideComps] = useState(false);

  useEffect(() => {
    if (content.data) {
      setHiddenGenres(content.data.hidden_genres);
      setHiddenLangs(content.data.hidden_languages);
      setHideComps(content.data.hide_compilations ?? false);
    }
  }, [content.data]);

  const dirty = content.data
    ? !sameSet(hiddenGenres, content.data.hidden_genres) ||
      !sameSet(hiddenLangs, content.data.hidden_languages) ||
      hideComps !== (content.data.hide_compilations ?? false)
    : false;

  const onSave = async () => {
    try {
      await update.mutateAsync({
        hidden_genres: hiddenGenres,
        hidden_languages: hiddenLangs,
        hide_compilations: hideComps,
      });
      toast.success('Настройки контента сохранены');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  const onReset = () => {
    if (content.data) {
      setHiddenGenres(content.data.hidden_genres);
      setHiddenLangs(content.data.hidden_languages);
      setHideComps(content.data.hide_compilations ?? false);
    }
  };

  const loading = content.isLoading || langsQ.isLoading || genresQ.isLoading;
  const failed = content.error || langsQ.error || genresQ.error;
  const hasLocked =
    (content.data?.admin_hidden_genres.length ?? 0) > 0 ||
    (content.data?.admin_hidden_languages.length ?? 0) > 0;

  return (
    <article className="space-y-6">
      <ProfileTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Контент</h1>
        <p className="text-sm text-muted-foreground">
          Отметьте языки и жанры, которые не хотите видеть. Скрытые книги исчезнут из вашего
          списка, поиска и фильтров. Настройка действует только для вас.
        </p>
        {hasLocked ? (
          <p className="text-xs text-muted-foreground">
            Пункты с замком скрыты администратором для всего сервера — их нельзя включить.
          </p>
        ) : null}
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
          lockedGenres={content.data?.admin_hidden_genres ?? []}
          lockedLanguages={content.data?.admin_hidden_languages ?? []}
          onChangeGenres={setHiddenGenres}
          onChangeLanguages={setHiddenLangs}
          footer={
            <>
              {/* Сборники — третья карточка формы (тот же паттерн «отметь →
                  Сохрани», что языки/жанры): базово сборники лишь уходят в
                  отдельную секцию карточки автора, этот крест прячет их совсем. */}
              <Card>
                <CardContent className="space-y-3 sm:max-w-md">
                  <h2 className="flex items-center gap-2 text-base font-semibold">
                    <Layers className="size-4" aria-hidden />
                    Сборники
                  </h2>
                  <HideRow
                    label="Скрывать сборники и антологии"
                    hidden={hideComps}
                    onToggle={() => setHideComps((v) => !v)}
                  />
                  <p className="text-xs text-muted-foreground text-pretty">
                    Авторские сборники, антологии и тома собраний сочинений исчезнут из
                    списка книг, поиска и карточек авторов. Прямые ссылки продолжат
                    открываться.
                  </p>
                </CardContent>
              </Card>
              {dirty ? (
                <SaveBar saving={update.isPending} onSave={onSave} onReset={onReset} />
              ) : null}
            </>
          }
        />
      )}
    </article>
  );
}
