import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { Flame, Square } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Skeleton } from '@/components/ui/skeleton';
import { AdminTabs } from '@/components/AdminTabs';
import { SaveBar } from '@/components/SaveBar';
import {
  useYearEnrichmentSettings,
  useUpdateYearEnrichmentSettings,
  useRunYearBackfill,
  useStopYearBackfill,
  type YearEnrichmentSettings,
  type YearEnrichmentInput,
} from '@/lib/admin';
import { ApiError } from '@/lib/api';

/**
 * AdminYearEnrichmentPage — /admin/year-enrichment. Дозаполнение
 * written_year из внешних источников (OpenLibrary → Wikidata) для книг,
 * у которых год не извлёкся из fb2. Две секции:
 *   - «Состояние и управление» — live-тумблер воркера, разовый прогон,
 *     покрытие written_year по источникам;
 *   - «Источники и лимиты» — live-тумблеры источников + rate-limit/TTL с
 *     контекстным «Сохранить».
 */
const SOURCE_LABELS: Record<string, string> = {
  fb2_title: 'из fb2',
  openlibrary: 'OpenLibrary',
  wikidata: 'Wikidata',
  googlebooks: 'Google Books',
  manual: 'вручную',
  unknown: 'прочее',
};

function buildInput(d: YearEnrichmentSettings, patch: Partial<YearEnrichmentInput>): YearEnrichmentInput {
  return {
    enabled: d.enabled,
    openlibrary: d.openlibrary,
    wikidata: d.wikidata,
    openlibrary_rpm: d.openlibrary_rpm,
    wikidata_rpm: d.wikidata_rpm,
    not_found_retry_days: d.not_found_retry_days,
    error_retry_hours: d.error_retry_hours,
    ...patch,
  };
}

export function AdminYearEnrichmentPage() {
  const q = useYearEnrichmentSettings();
  const update = useUpdateYearEnrichmentSettings();
  const runNow = useRunYearBackfill();
  const stop = useStopYearBackfill();

  const running = q.data?.year_backfill_running ?? false;
  const mode = q.data?.year_backfill_mode ?? 'off';
  const enabled = q.data?.enabled ?? false;

  // Числовые лимиты — поля формы (init ОДИН раз: поллинг не затирает правки).
  const [olRpm, setOlRpm] = useState('');
  const [wdRpm, setWdRpm] = useState('');
  const [nfDays, setNfDays] = useState('');
  const [errHours, setErrHours] = useState('');
  const initialized = useRef(false);
  useEffect(() => {
    if (q.data && !initialized.current) {
      setOlRpm(String(q.data.openlibrary_rpm));
      setWdRpm(String(q.data.wikidata_rpm));
      setNfDays(String(q.data.not_found_retry_days));
      setErrHours(String(q.data.error_retry_hours));
      initialized.current = true;
    }
  }, [q.data]);

  const nums = { ol: Number(olRpm), wd: Number(wdRpm), nf: Number(nfDays), eh: Number(errHours) };
  const invalid =
    [olRpm, wdRpm, nfDays, errHours].some((s) => s === '') ||
    Object.values(nums).some((n) => Number.isNaN(n) || n < 0);
  const dirty =
    !!q.data &&
    (olRpm !== String(q.data.openlibrary_rpm) ||
      wdRpm !== String(q.data.wikidata_rpm) ||
      nfDays !== String(q.data.not_found_retry_days) ||
      errHours !== String(q.data.error_retry_hours));

  // Live-применение тумблеров (воркер / источники): шлём полный конфиг с
  // СОХРАНЁННЫМИ числами (q.data), не трогая несохранённые правки в полях.
  const apply = async (patch: Partial<YearEnrichmentInput>, msg: string) => {
    if (!q.data) return;
    try {
      await update.mutateAsync(buildInput(q.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };

  const onReset = () => {
    if (q.data) {
      setOlRpm(String(q.data.openlibrary_rpm));
      setWdRpm(String(q.data.wikidata_rpm));
      setNfDays(String(q.data.not_found_retry_days));
      setErrHours(String(q.data.error_retry_hours));
    }
  };

  const onSave = async () => {
    if (invalid || !q.data) return;
    try {
      const saved = await update.mutateAsync(
        buildInput(q.data, {
          openlibrary_rpm: nums.ol,
          wikidata_rpm: nums.wd,
          not_found_retry_days: nums.nf,
          error_retry_hours: nums.eh,
        }),
      );
      setOlRpm(String(saved.openlibrary_rpm));
      setWdRpm(String(saved.wikidata_rpm));
      setNfDays(String(saved.not_found_retry_days));
      setErrHours(String(saved.error_retry_hours));
      toast.success('Лимиты сохранены');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  const onRunNow = async () => {
    try {
      await runNow.mutateAsync();
      toast.success('Дозаполнение запущено в фоне');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось запустить');
    }
  };
  const onStop = async () => {
    try {
      await stop.mutateAsync();
      toast.success('Останавливаю…');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось остановить');
    }
  };

  const cov = q.data?.coverage;
  const pct = cov && cov.total > 0 ? Math.round((cov.with_year / cov.total) * 100) : 0;

  return (
    <article className="space-y-6 text-pretty">
      <AdminTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Год издания</h1>
        <p className="text-sm text-muted-foreground">
          Год написания берётся из fb2; для книг без него можно дозаполнить из внешних
          источников (OpenLibrary → Wikidata). Воркер ходит в публичные API, поэтому
          включается вручную и работает с ограничением скорости.
        </p>
      </header>

      {q.isLoading ? (
        <Skeleton className="h-64 w-full" />
      ) : q.error ? (
        <p className="text-sm text-destructive">Не удалось загрузить настройки.</p>
      ) : (
        <>
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Состояние и управление</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 pt-2 sm:max-w-md">
              <div className="space-y-2">
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="year-enabled"
                    checked={enabled}
                    disabled={update.isPending}
                    onCheckedChange={(v) =>
                      void apply({ enabled: v }, v ? 'Фоновое дозаполнение включено' : 'Выключено')
                    }
                  />
                  <Label htmlFor="year-enabled" className="cursor-pointer text-sm">
                    Фоновое дозаполнение года из внешних источников
                  </Label>
                </div>
                <p className="text-xs text-muted-foreground">
                  Опрашивает OpenLibrary и Wikidata для книг без года из fb2. Применяется сразу.
                </p>
                {running ? (
                  <p className="flex items-center gap-2 text-xs text-muted-foreground">
                    <span className="inline-block size-2 animate-pulse rounded-full bg-primary" aria-hidden />
                    {mode === 'continuous' ? 'Непрерывный воркер активен.' : 'Идёт разовый проход…'}
                  </p>
                ) : null}
              </div>

              <div className="flex flex-wrap gap-2">
                {mode === 'once' ? (
                  <Button variant="outline" onClick={onStop} disabled={stop.isPending}>
                    <Square className="size-4" aria-hidden />
                    {stop.isPending ? 'Остановка…' : 'Остановить проход'}
                  </Button>
                ) : (
                  <Button
                    variant="outline"
                    onClick={onRunNow}
                    disabled={enabled || running || runNow.isPending}
                  >
                    <Flame className="size-4" aria-hidden />
                    {runNow.isPending ? 'Запуск…' : 'Запустить сейчас'}
                  </Button>
                )}
              </div>
              {enabled && !running ? (
                <p className="-mt-2 text-xs text-muted-foreground">
                  «Запустить сейчас» недоступно при включённом фоновом воркере — он и так
                  обрабатывает всю коллекцию.
                </p>
              ) : null}

              <div className="grid grid-cols-2 gap-3 border-t border-border pt-3 text-sm">
                <span className="text-muted-foreground">Год известен</span>
                <span className="tabular-nums">
                  {cov ? `${cov.with_year} из ${cov.total} (${pct}%)` : '—'}
                </span>
              </div>
              {cov && Object.keys(cov.by_source).length > 0 ? (
                <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  {Object.entries(cov.by_source)
                    .sort((a, b) => b[1] - a[1])
                    .map(([src, n]) => (
                      <span key={src} className="tabular-nums">
                        {SOURCE_LABELS[src] ?? src}: {n}
                      </span>
                    ))}
                </div>
              ) : null}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Источники и лимиты</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 pt-2 sm:max-w-md">
              <div className="space-y-2">
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="src-ol"
                    checked={q.data?.openlibrary ?? false}
                    disabled={update.isPending}
                    onCheckedChange={(v) => void apply({ openlibrary: v }, 'Применено')}
                  />
                  <Label htmlFor="src-ol" className="cursor-pointer text-sm">
                    OpenLibrary (first_publish_year)
                  </Label>
                </div>
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="src-wd"
                    checked={q.data?.wikidata ?? false}
                    disabled={update.isPending}
                    onCheckedChange={(v) => void apply({ wikidata: v }, 'Применено')}
                  />
                  <Label htmlFor="src-wd" className="cursor-pointer text-sm">
                    Wikidata (P577)
                  </Label>
                </div>
                <p className="text-xs text-muted-foreground">
                  Приоритет фиксирован: сначала OpenLibrary, затем Wikidata. Тумблеры
                  применяются сразу.
                </p>
              </div>

              <div className="grid grid-cols-2 gap-3 border-t border-border pt-3">
                <div className="space-y-1.5">
                  <Label htmlFor="ol-rpm">OpenLibrary, запросов/мин</Label>
                  <Input id="ol-rpm" type="number" min={0} value={olRpm} onChange={(e) => setOlRpm(e.target.value)} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="wd-rpm">Wikidata, запросов/мин</Label>
                  <Input id="wd-rpm" type="number" min={0} value={wdRpm} onChange={(e) => setWdRpm(e.target.value)} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="nf-days">Ретрай «не найдено», дней</Label>
                  <Input id="nf-days" type="number" min={0} value={nfDays} onChange={(e) => setNfDays(e.target.value)} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="err-hours">Ретрай ошибки, часов</Label>
                  <Input id="err-hours" type="number" min={0} value={errHours} onChange={(e) => setErrHours(e.target.value)} />
                </div>
              </div>
              <p className="text-xs text-muted-foreground">
                0 запросов/мин — без ограничения скорости (не рекомендуется для публичных API).
                TTL ретраев — как часто перепроверять источник, ранее вернувший «не найдено» / ошибку.
              </p>
            </CardContent>
          </Card>

          {dirty ? (
            <SaveBar saving={update.isPending} onSave={onSave} onReset={onReset} canSave={!invalid} />
          ) : null}
        </>
      )}
    </article>
  );
}
