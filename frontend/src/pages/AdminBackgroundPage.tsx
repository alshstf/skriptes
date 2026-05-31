import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { AlertTriangle, Flame, Info, Square, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Skeleton } from '@/components/ui/skeleton';
import { AdminTabs } from '@/components/AdminTabs';
import { SaveBar } from '@/components/SaveBar';
import {
  useCoverCacheSettings,
  useUpdateCoverCacheSettings,
  useClearCoverCache,
  usePrewarmCoverCache,
  useStopPrewarmCoverCache,
  useYearEnrichmentSettings,
  useUpdateYearEnrichmentSettings,
  useRunYearBackfill,
  useStopYearBackfill,
  type CoverCacheSettings,
  type CollectionInput,
  type Intensity,
  type YearEnrichmentSettings,
  type YearEnrichmentInput,
} from '@/lib/admin';
import { ApiError } from '@/lib/api';

/**
 * AdminBackgroundPage — /admin/background. Единое место для фоновых операций
 * вместо разрозненных «Кэш обложек» и «Год издания»:
 *   - Секция 1 «Обработка коллекции» — парсинг fb2 (локально): мастер-тумблер +
 *     под-тумблеры обложки/аннотации/года, лимиты кэша, интенсивность IO.
 *   - Секция 2 «Внешние источники» — фоновый опрос OpenLibrary/Wikidata (пока
 *     только года; обложки/био/экранизации — следующей фазой).
 * Один SaveBar на странице — появляется при изменении числовых полей любой секции.
 */
function formatBytes(n: number): string {
  if (n < 0) return '—';
  if (n < 1024) return `${n} Б`;
  const units = ['КБ', 'МБ', 'ГБ', 'ТБ'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)} ${units[i]}`;
}

const MIN_FREE_WARN_MB = 100;

const SOURCE_LABELS: Record<string, string> = {
  fb2_title: 'из fb2',
  openlibrary: 'OpenLibrary',
  wikidata: 'Wikidata',
  googlebooks: 'Google Books',
  manual: 'вручную',
  unknown: 'прочее',
};

const INTENSITY_LABELS: Record<Intensity, string> = {
  low: 'Низкая',
  medium: 'Средняя',
  high: 'Высокая',
};

function buildCollectionInput(d: CoverCacheSettings, patch: Partial<CollectionInput>): CollectionInput {
  return {
    cache_max_mb: d.cache_max_mb,
    cache_min_free_mb: d.cache_min_free_mb,
    prewarm: d.prewarm,
    sync_covers: d.sync_covers,
    sync_annotations: d.sync_annotations,
    sync_years: d.sync_years,
    intensity: d.intensity,
    ...patch,
  };
}

function buildYearInput(d: YearEnrichmentSettings, patch: Partial<YearEnrichmentInput>): YearEnrichmentInput {
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

export function AdminBackgroundPage() {
  // ── Секция 1: обработка коллекции ──
  const cq = useCoverCacheSettings();
  const updateCol = useUpdateCoverCacheSettings();
  const clear = useClearCoverCache();
  const runCol = usePrewarmCoverCache();
  const stopCol = useStopPrewarmCoverCache();

  const master = cq.data?.prewarm ?? false;
  const colRunning = cq.data?.prewarm_running ?? false;
  const colMode = cq.data?.prewarm_mode ?? 'off';

  const [maxMB, setMaxMB] = useState('');
  const [minFreeMB, setMinFreeMB] = useState('');
  const colInit = useRef(false);
  useEffect(() => {
    if (cq.data && !colInit.current) {
      setMaxMB(String(cq.data.cache_max_mb));
      setMinFreeMB(String(cq.data.cache_min_free_mb));
      colInit.current = true;
    }
  }, [cq.data]);

  const maxN = Number(maxMB);
  const minFreeN = Number(minFreeMB);
  const colInvalid =
    maxMB === '' || minFreeMB === '' || Number.isNaN(maxN) || Number.isNaN(minFreeN) || maxN < 0 || minFreeN < 0;
  const lowFloorWarn = !Number.isNaN(minFreeN) && minFreeN < MIN_FREE_WARN_MB;
  const colDirty =
    !!cq.data && (maxMB !== String(cq.data.cache_max_mb) || minFreeMB !== String(cq.data.cache_min_free_mb));

  // Live-применение тумблеров/интенсивности (с сохранёнными лимитами из cq.data).
  const applyCol = async (patch: Partial<CollectionInput>, msg: string) => {
    if (!cq.data) return;
    try {
      await updateCol.mutateAsync(buildCollectionInput(cq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };

  // ── Секция 2: внешние источники (года) ──
  const yq = useYearEnrichmentSettings();
  const updateYear = useUpdateYearEnrichmentSettings();
  const runYear = useRunYearBackfill();
  const stopYear = useStopYearBackfill();

  const extEnabled = yq.data?.enabled ?? false;
  const extRunning = yq.data?.year_backfill_running ?? false;
  const extMode = yq.data?.year_backfill_mode ?? 'off';

  const [olRpm, setOlRpm] = useState('');
  const [wdRpm, setWdRpm] = useState('');
  const [nfDays, setNfDays] = useState('');
  const [errHours, setErrHours] = useState('');
  const yearInit = useRef(false);
  useEffect(() => {
    if (yq.data && !yearInit.current) {
      setOlRpm(String(yq.data.openlibrary_rpm));
      setWdRpm(String(yq.data.wikidata_rpm));
      setNfDays(String(yq.data.not_found_retry_days));
      setErrHours(String(yq.data.error_retry_hours));
      yearInit.current = true;
    }
  }, [yq.data]);

  const yNums = { ol: Number(olRpm), wd: Number(wdRpm), nf: Number(nfDays), eh: Number(errHours) };
  const yearInvalid =
    [olRpm, wdRpm, nfDays, errHours].some((s) => s === '') ||
    Object.values(yNums).some((n) => Number.isNaN(n) || n < 0);
  const yearDirty =
    !!yq.data &&
    (olRpm !== String(yq.data.openlibrary_rpm) ||
      wdRpm !== String(yq.data.wikidata_rpm) ||
      nfDays !== String(yq.data.not_found_retry_days) ||
      errHours !== String(yq.data.error_retry_hours));

  const applyYear = async (patch: Partial<YearEnrichmentInput>, msg: string) => {
    if (!yq.data) return;
    try {
      await updateYear.mutateAsync(buildYearInput(yq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };

  // ── Общий SaveBar (числовые поля обеих секций) ──
  const dirty = colDirty || yearDirty;
  const saveInvalid = (colDirty && colInvalid) || (yearDirty && yearInvalid);
  const saving = updateCol.isPending || updateYear.isPending;

  const onReset = () => {
    if (cq.data) {
      setMaxMB(String(cq.data.cache_max_mb));
      setMinFreeMB(String(cq.data.cache_min_free_mb));
    }
    if (yq.data) {
      setOlRpm(String(yq.data.openlibrary_rpm));
      setWdRpm(String(yq.data.wikidata_rpm));
      setNfDays(String(yq.data.not_found_retry_days));
      setErrHours(String(yq.data.error_retry_hours));
    }
  };

  const onSave = async () => {
    try {
      if (colDirty && !colInvalid && cq.data) {
        const saved = await updateCol.mutateAsync(
          buildCollectionInput(cq.data, { cache_max_mb: maxN, cache_min_free_mb: minFreeN }),
        );
        setMaxMB(String(saved.cache_max_mb));
        setMinFreeMB(String(saved.cache_min_free_mb));
      }
      if (yearDirty && !yearInvalid && yq.data) {
        const saved = await updateYear.mutateAsync(
          buildYearInput(yq.data, {
            openlibrary_rpm: yNums.ol,
            wikidata_rpm: yNums.wd,
            not_found_retry_days: yNums.nf,
            error_retry_hours: yNums.eh,
          }),
        );
        setOlRpm(String(saved.openlibrary_rpm));
        setWdRpm(String(saved.wikidata_rpm));
        setNfDays(String(saved.not_found_retry_days));
        setErrHours(String(saved.error_retry_hours));
      }
      toast.success('Сохранено');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  // ── Действия секции 1 ──
  const onClear = async () => {
    if (!window.confirm('Очистить весь кэш обложек? Они переизвлекутся из fb2 по мере просмотра.')) return;
    try {
      const r = await clear.mutateAsync();
      toast.success(`Кэш очищен: удалено файлов — ${r.removed}`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось очистить');
    }
  };
  const onRunCol = async () => {
    try {
      await runCol.mutateAsync();
      toast.success('Обработка запущена в фоне');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось запустить');
    }
  };
  const onStopCol = async () => {
    try {
      await stopCol.mutateAsync();
      toast.success('Останавливаю…');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось остановить');
    }
  };

  // ── Действия секции 2 ──
  const onRunYear = async () => {
    try {
      await runYear.mutateAsync();
      toast.success('Дозаполнение запущено в фоне');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось запустить');
    }
  };
  const onStopYear = async () => {
    try {
      await stopYear.mutateAsync();
      toast.success('Останавливаю…');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось остановить');
    }
  };

  const cov = yq.data?.coverage;
  const pct = cov && cov.total > 0 ? Math.round((cov.with_year / cov.total) * 100) : 0;
  const loading = cq.isLoading || yq.isLoading;
  const failed = cq.error || yq.error;

  return (
    <article className="space-y-6 text-pretty">
      <AdminTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Фоновые операции</h1>
        <p className="text-sm text-muted-foreground">
          Обработка локальной коллекции (парсинг fb2) и фоновый опрос внешних источников.
        </p>
      </header>

      {loading ? (
        <Skeleton className="h-64 w-full" />
      ) : failed ? (
        <p className="text-sm text-destructive">Не удалось загрузить настройки.</p>
      ) : (
        <>
          {/* ─────────── Секция 1: обработка коллекции ─────────── */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Обработка коллекции</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 pt-2 sm:max-w-md">
              <div className="flex items-center gap-2.5">
                <Switch
                  id="col-master"
                  checked={master}
                  disabled={updateCol.isPending}
                  onCheckedChange={(v) => void applyCol({ prewarm: v }, v ? 'Обработка включена' : 'Выключена')}
                />
                <Label htmlFor="col-master" className="cursor-pointer text-sm font-medium">
                  Фоновая обработка коллекции (из fb2)
                </Label>
              </div>
              <p className="text-xs text-muted-foreground">
                Перебирает книги и извлекает из fb2 выбранное ниже. Локально, без обращения к сети.
              </p>

              {/* Под-тумблеры — что синкать. Disabled при выключенном мастере. */}
              <div className="space-y-3 border-l border-border pl-3">
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="sync-covers"
                    checked={cq.data?.sync_covers ?? false}
                    disabled={!master || updateCol.isPending}
                    onCheckedChange={(v) => void applyCol({ sync_covers: v }, 'Применено')}
                  />
                  <Label htmlFor="sync-covers" className="cursor-pointer text-sm">Обложки</Label>
                </div>

                <div className="flex items-center gap-2.5">
                  <Switch
                    id="sync-annotations"
                    checked={cq.data?.sync_annotations ?? false}
                    disabled={!master || updateCol.isPending}
                    onCheckedChange={(v) => void applyCol({ sync_annotations: v }, 'Применено')}
                  />
                  <Label htmlFor="sync-annotations" className="cursor-pointer text-sm">Аннотации</Label>
                </div>

                <div className="flex items-center gap-2.5">
                  <Switch
                    id="sync-years"
                    checked={cq.data?.sync_years ?? false}
                    disabled={!master || updateCol.isPending}
                    onCheckedChange={(v) => void applyCol({ sync_years: v }, 'Применено')}
                  />
                  <Label htmlFor="sync-years" className="cursor-pointer text-sm">
                    Года написания и издания
                  </Label>
                </div>
              </div>

              {/* Интенсивность (троттлинг IO) — применяется сразу. */}
              <div className="space-y-1.5">
                <Label>Интенсивность IO</Label>
                <div className="flex gap-1">
                  {(['low', 'medium', 'high'] as Intensity[]).map((lvl) => (
                    <Button
                      key={lvl}
                      type="button"
                      size="sm"
                      variant={(cq.data?.intensity ?? 'medium') === lvl ? 'default' : 'outline'}
                      disabled={updateCol.isPending}
                      onClick={() => void applyCol({ intensity: lvl }, 'Интенсивность применена')}
                    >
                      {INTENSITY_LABELS[lvl]}
                    </Button>
                  ))}
                </div>
                <p className="text-xs text-muted-foreground">
                  Нагрузка на диск: ниже — медленнее, но щадит IO (для HDD); выше — быстрее (для NVMe).
                </p>
              </div>

              {/* Действия */}
              <div className="flex flex-wrap gap-2">
                {colMode === 'once' ? (
                  <Button variant="outline" onClick={onStopCol} disabled={stopCol.isPending}>
                    <Square className="size-4" aria-hidden />
                    {stopCol.isPending ? 'Остановка…' : 'Остановить проход'}
                  </Button>
                ) : (
                  <Button variant="outline" onClick={onRunCol} disabled={master || colRunning || runCol.isPending}>
                    <Flame className="size-4" aria-hidden />
                    {runCol.isPending ? 'Запуск…' : 'Прогнать разово'}
                  </Button>
                )}
                <Button variant="outline" onClick={onClear} disabled={clear.isPending}>
                  <Trash2 className="size-4" aria-hidden />
                  {clear.isPending ? 'Очистка…' : 'Очистить кэш'}
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                «Прогнать разово» — однократный проход по непрогретым книгам. Постоянную фоновую
                обработку включает мастер-тумблер выше.
              </p>
              {colRunning ? (
                <p className="flex items-center gap-2 text-xs text-muted-foreground">
                  <span className="inline-block size-2 animate-pulse rounded-full bg-primary" aria-hidden />
                  {colMode === 'continuous' ? 'Непрерывная обработка активна.' : 'Идёт разовый проход…'}
                </p>
              ) : null}

              {/* Кэш обложек — отдельно от тумблеров обработки: бюджет и порог
                  действуют не только на фоновую джобу, но и на lazy-кэш
                  (извлечение обложки при первом открытии). */}
              <div className="space-y-3 border-t border-border pt-3">
                <p className="text-sm font-medium">Кэш обложек</p>
                <div className="space-y-1.5">
                  <Label htmlFor="cache-max">Бюджет кэша, МБ</Label>
                  <Input
                    id="cache-max"
                    type="number"
                    min={0}
                    value={maxMB}
                    disabled={master}
                    onChange={(e) => setMaxMB(e.target.value)}
                  />
                  {master ? (
                    <Callout icon={<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
                      Бюджет не применяется при включённой обработке коллекции — рост кэша
                      ограничивает только порог свободного места.
                    </Callout>
                  ) : (
                    <p className="text-xs text-muted-foreground">
                      При превышении вытесняются давно не запрашивавшиеся (LRU). 0 — без лимита.
                    </p>
                  )}
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="cache-min-free">Порог свободного места, МБ</Label>
                  <Input
                    id="cache-min-free"
                    type="number"
                    min={0}
                    value={minFreeMB}
                    onChange={(e) => setMinFreeMB(e.target.value)}
                  />
                  {lowFloorWarn ? (
                    <Callout icon={<AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
                      Безопаснее держать ≥ {MIN_FREE_WARN_MB} МБ: слишком низкий порог повышает
                      риск забить диск.
                    </Callout>
                  ) : null}
                </div>
                <div className="grid grid-cols-2 gap-3 text-sm">
                  <span className="text-muted-foreground">Размер кэша</span>
                  <span className="tabular-nums">{formatBytes(cq.data?.cache_size_bytes ?? 0)}</span>
                  <span className="text-muted-foreground">Свободно на диске</span>
                  <span className="tabular-nums">{formatBytes(cq.data?.free_bytes ?? -1)}</span>
                </div>
              </div>
            </CardContent>
          </Card>

          {/* ─────────── Секция 2: внешние источники ─────────── */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Внешние источники</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 pt-2 sm:max-w-md">
              <div className="flex items-center gap-2.5">
                <Switch
                  id="ext-master"
                  checked={extEnabled}
                  disabled={updateYear.isPending}
                  onCheckedChange={(v) =>
                    void applyYear({ enabled: v }, v ? 'Фоновое дозаполнение включено' : 'Выключено')
                  }
                />
                <Label htmlFor="ext-master" className="cursor-pointer text-sm font-medium">
                  Фоновое дозаполнение из OpenLibrary / Wikidata
                </Label>
              </div>
              <p className="text-xs text-muted-foreground">
                Тянет год для книг без него из fb2. Если обложки/года из fb2 включены — внешние работают
                как фолбэк (где локально не нашлось); если выключены — для всей коллекции (долго). Ходит в
                публичные API, поэтому с ограничением скорости.
              </p>

              <div className="space-y-2 border-l border-border pl-3">
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="src-ol"
                    checked={yq.data?.openlibrary ?? false}
                    disabled={updateYear.isPending}
                    onCheckedChange={(v) => void applyYear({ openlibrary: v }, 'Применено')}
                  />
                  <Label htmlFor="src-ol" className="cursor-pointer text-sm">Годы — OpenLibrary (first_publish_year)</Label>
                </div>
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="src-wd"
                    checked={yq.data?.wikidata ?? false}
                    disabled={updateYear.isPending}
                    onCheckedChange={(v) => void applyYear({ wikidata: v }, 'Применено')}
                  />
                  <Label htmlFor="src-wd" className="cursor-pointer text-sm">Годы — Wikidata (P577)</Label>
                </div>
                <p className="text-xs text-muted-foreground">
                  Обложки / биографии / экранизации из внешних источников фоном — следующей фазой (сейчас
                  только лениво при открытии).
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

              <div className="flex flex-wrap gap-2">
                {extMode === 'once' ? (
                  <Button variant="outline" onClick={onStopYear} disabled={stopYear.isPending}>
                    <Square className="size-4" aria-hidden />
                    {stopYear.isPending ? 'Остановка…' : 'Остановить проход'}
                  </Button>
                ) : (
                  <Button variant="outline" onClick={onRunYear} disabled={extEnabled || extRunning || runYear.isPending}>
                    <Flame className="size-4" aria-hidden />
                    {runYear.isPending ? 'Запуск…' : 'Прогнать разово'}
                  </Button>
                )}
              </div>
              <p className="text-xs text-muted-foreground">
                «Прогнать разово» — однократный проход; постоянную работу включает тумблер выше.
              </p>
              {extRunning ? (
                <p className="flex items-center gap-2 text-xs text-muted-foreground">
                  <span className="inline-block size-2 animate-pulse rounded-full bg-primary" aria-hidden />
                  {extMode === 'continuous' ? 'Непрерывный воркер активен.' : 'Идёт разовый проход…'}
                </p>
              ) : null}

              <div className="grid grid-cols-2 gap-3 border-t border-border pt-3 text-sm">
                <span className="text-muted-foreground">Год известен</span>
                <span className="tabular-nums">{cov ? `${cov.with_year} из ${cov.total} (${pct}%)` : '—'}</span>
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

          {dirty ? (
            <SaveBar saving={saving} onSave={onSave} onReset={onReset} canSave={!saveInvalid} />
          ) : null}
        </>
      )}
    </article>
  );
}
