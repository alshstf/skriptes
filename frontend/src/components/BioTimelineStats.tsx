/**
 * BioTimelineStats — карточка «Био-таймлайн» в «Фоновых операциях»: версия
 * встроенного датасета, посеянная версия, покрытие авторов каталога
 * (с QID / с таймлайном), скрытые вехи. Внешних вызовов у фичи нет —
 * управлять нечем, только смотреть.
 */
import { useBioTimelineStats } from '@/lib/bioTimeline';

export function BioTimelineStats() {
  const { data, isError } = useBioTimelineStats();
  return (
    <section className="rounded-lg border border-border bg-card p-4" data-testid="bio-timeline-stats">
      <h2 className="mb-1 text-base font-medium">Био-таймлайн авторов</h2>
      <p className="mb-3 text-xs text-muted-foreground text-pretty">
        Вехи жизни — курируемый датасет в репозитории (ключ — Wikidata QID автора), сеется в базу на старте при смене
        версии. Внешних запросов нет; правки — через датасет, скрыть веху можно на странице таймлайна в режиме правки.
      </p>
      {isError ? (
        <p className="text-sm text-destructive">Не удалось загрузить статистику.</p>
      ) : !data ? (
        <p className="text-sm text-muted-foreground">Загрузка…</p>
      ) : (
        <dl className="grid grid-cols-2 gap-x-6 gap-y-1 text-sm sm:max-w-md">
          <dt className="text-muted-foreground">Авторов в датасете</dt>
          <dd className="tabular-nums">{data.authors_in_dataset}</dd>
          <dt className="text-muted-foreground">Авторов каталога с QID</dt>
          <dd className="tabular-nums">{data.authors_with_qid}</dd>
          <dt className="text-muted-foreground">Из них с таймлайном</dt>
          <dd className="tabular-nums">{data.authors_with_timeline}</dd>
          <dt className="text-muted-foreground">Скрытых вех</dt>
          <dd className="tabular-nums">{data.hidden_events}</dd>
          <dt className="text-muted-foreground">Версия датасета</dt>
          <dd className="font-mono text-xs">
            {data.dataset_version}
            {data.seeded_version && data.seeded_version !== data.dataset_version ? (
              <span className="text-muted-foreground"> (в базе {data.seeded_version} — сев ещё идёт)</span>
            ) : null}
          </dd>
        </dl>
      )}
    </section>
  );
}
