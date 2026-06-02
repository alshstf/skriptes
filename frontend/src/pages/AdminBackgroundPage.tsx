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
  useClearPosterCache,
  useClearPhotoCache,
  usePrewarmCoverCache,
  useStopPrewarmCoverCache,
  useYearEnrichmentSettings,
  useUpdateYearEnrichmentSettings,
  useRunYearBackfill,
  useStopYearBackfill,
  useCoverEnrichmentSettings,
  useUpdateCoverEnrichmentSettings,
  useRunCoverBackfill,
  useStopCoverBackfill,
  useBioAdaptationSettings,
  useUpdateBioAdaptationSettings,
  useRunBioBackfill,
  useStopBioBackfill,
  useRunAdaptationBackfill,
  useStopAdaptationBackfill,
  type CoverCacheSettings,
  type CollectionInput,
  type Intensity,
  type YearEnrichmentSettings,
  type YearEnrichmentInput,
  type CoverEnrichmentSettings,
  type CoverEnrichmentInput,
  type BioAdaptationSettings,
  type BioAdaptationInput,
} from '@/lib/admin';
import { ApiError } from '@/lib/api';

/**
 * AdminBackgroundPage — /admin/background. Единое место для фоновых операций
 * вместо разрозненных «Кэш обложек» и «Год издания»:
 *   - Секция 1 «Обработка коллекции» — парсинг fb2 (локально): мастер-тумблер +
 *     под-тумблеры обложки/аннотации/года, лимиты кэша, интенсивность IO.
 *   - Секция 2 «Внешние источники» — фоновый опрос OpenLibrary/Wikidata/Google
 *     Books: годы (OL → Wikidata) и обложки (OL → Google Books). У каждого типа
 *     данных режим охвата: фолбэк (где fb2 не дал) или вся коллекция (долго).
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
    poster_cache_max_mb: d.poster_cache_max_mb,
    photo_cache_max_mb: d.photo_cache_max_mb,
    ...patch,
  };
}

function buildYearInput(d: YearEnrichmentSettings, patch: Partial<YearEnrichmentInput>): YearEnrichmentInput {
  return {
    enabled: d.enabled,
    openlibrary: d.openlibrary,
    wikidata: d.wikidata,
    whole_collection: d.whole_collection,
    openlibrary_rpm: d.openlibrary_rpm,
    wikidata_rpm: d.wikidata_rpm,
    not_found_retry_days: d.not_found_retry_days,
    error_retry_hours: d.error_retry_hours,
    ...patch,
  };
}

function buildCoverInput(d: CoverEnrichmentSettings, patch: Partial<CoverEnrichmentInput>): CoverEnrichmentInput {
  return {
    enabled: d.enabled,
    openlibrary: d.openlibrary,
    googlebooks: d.googlebooks,
    whole_collection: d.whole_collection,
    openlibrary_rpm: d.openlibrary_rpm,
    googlebooks_rpm: d.googlebooks_rpm,
    not_found_retry_days: d.not_found_retry_days,
    error_retry_hours: d.error_retry_hours,
    ...patch,
  };
}

function buildBaInput(d: BioAdaptationSettings, patch: Partial<BioAdaptationInput>): BioAdaptationInput {
  return {
    bios: d.bios,
    adaptations: d.adaptations,
    bios_rpm: d.bios_rpm,
    adaptations_rpm: d.adaptations_rpm,
    ...patch,
  };
}

// WholeCollectionSwitch — переключатель режима охвата внешнего источника
// (фолбэк ↔ вся коллекция) с дисклеймером при включении. Общий для годов и
// обложек.
function WholeCollectionSwitch({
  id,
  checked,
  disabled,
  onChange,
  warning,
}: {
  id: string;
  checked: boolean;
  disabled: boolean;
  onChange: (v: boolean) => void;
  warning: string;
}) {
  return (
    <div className="space-y-2 border-t border-border pt-3">
      <div className="flex items-center gap-2.5">
        <Switch id={id} checked={checked} disabled={disabled} onCheckedChange={onChange} />
        <Label htmlFor={id} className="cursor-pointer text-sm">
          Вся коллекция (иначе только где fb2 не дал)
        </Label>
      </div>
      {checked ? (
        <Callout icon={<AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>{warning}</Callout>
      ) : (
        <p className="text-xs text-muted-foreground">
          Фолбэк: дозаполняются только книги, у которых локальный fb2-проход уже прошёл, но данных не дал.
        </p>
      )}
    </div>
  );
}

export function AdminBackgroundPage() {
  // ── Секция 1: обработка коллекции ──
  const cq = useCoverCacheSettings();
  const updateCol = useUpdateCoverCacheSettings();
  const clear = useClearCoverCache();
  const clearPosters = useClearPosterCache();
  const clearPhotos = useClearPhotoCache();
  const runCol = usePrewarmCoverCache();
  const stopCol = useStopPrewarmCoverCache();

  const master = cq.data?.prewarm ?? false;
  const colRunning = cq.data?.prewarm_running ?? false;
  const colMode = cq.data?.prewarm_mode ?? 'off';

  const [maxMB, setMaxMB] = useState('');
  const [minFreeMB, setMinFreeMB] = useState('');
  const [posterMB, setPosterMB] = useState('');
  const [photoMB, setPhotoMB] = useState('');
  const colInit = useRef(false);
  useEffect(() => {
    if (cq.data && !colInit.current) {
      setMaxMB(String(cq.data.cache_max_mb));
      setMinFreeMB(String(cq.data.cache_min_free_mb));
      setPosterMB(String(cq.data.poster_cache_max_mb));
      setPhotoMB(String(cq.data.photo_cache_max_mb));
      colInit.current = true;
    }
  }, [cq.data]);

  const maxN = Number(maxMB);
  const minFreeN = Number(minFreeMB);
  const posterN = Number(posterMB);
  const photoN = Number(photoMB);
  const colInvalid =
    [maxMB, minFreeMB, posterMB, photoMB].some((s) => s === '') ||
    [maxN, minFreeN, posterN, photoN].some((n) => Number.isNaN(n) || n < 0);
  const lowFloorWarn = !Number.isNaN(minFreeN) && minFreeN < MIN_FREE_WARN_MB;
  const colDirty =
    !!cq.data &&
    (maxMB !== String(cq.data.cache_max_mb) ||
      minFreeMB !== String(cq.data.cache_min_free_mb) ||
      posterMB !== String(cq.data.poster_cache_max_mb) ||
      photoMB !== String(cq.data.photo_cache_max_mb));

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

  // ── Секция 2а: внешние источники — годы ──
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

  // ── Секция 2б: внешние источники — обложки ──
  const xq = useCoverEnrichmentSettings();
  const updateCover = useUpdateCoverEnrichmentSettings();
  const runCover = useRunCoverBackfill();
  const stopCover = useStopCoverBackfill();

  const covEnabled = xq.data?.enabled ?? false;
  const covRunning = xq.data?.cover_backfill_running ?? false;
  const covMode = xq.data?.cover_backfill_mode ?? 'off';

  const [olRpmC, setOlRpmC] = useState('');
  const [gbRpmC, setGbRpmC] = useState('');
  const [nfDaysC, setNfDaysC] = useState('');
  const [errHoursC, setErrHoursC] = useState('');
  const coverInit = useRef(false);
  useEffect(() => {
    if (xq.data && !coverInit.current) {
      setOlRpmC(String(xq.data.openlibrary_rpm));
      setGbRpmC(String(xq.data.googlebooks_rpm));
      setNfDaysC(String(xq.data.not_found_retry_days));
      setErrHoursC(String(xq.data.error_retry_hours));
      coverInit.current = true;
    }
  }, [xq.data]);

  const cNums = { ol: Number(olRpmC), gb: Number(gbRpmC), nf: Number(nfDaysC), eh: Number(errHoursC) };
  const coverInvalid =
    [olRpmC, gbRpmC, nfDaysC, errHoursC].some((s) => s === '') ||
    Object.values(cNums).some((n) => Number.isNaN(n) || n < 0);
  const coverDirty =
    !!xq.data &&
    (olRpmC !== String(xq.data.openlibrary_rpm) ||
      gbRpmC !== String(xq.data.googlebooks_rpm) ||
      nfDaysC !== String(xq.data.not_found_retry_days) ||
      errHoursC !== String(xq.data.error_retry_hours));

  const applyCover = async (patch: Partial<CoverEnrichmentInput>, msg: string) => {
    if (!xq.data) return;
    try {
      await updateCover.mutateAsync(buildCoverInput(xq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };

  // ── Секция 2в: внешние источники — биографии + экранизации ──
  const bq = useBioAdaptationSettings();
  const updateBa = useUpdateBioAdaptationSettings();
  const runBio = useRunBioBackfill();
  const stopBio = useStopBioBackfill();
  const runAdapt = useRunAdaptationBackfill();
  const stopAdapt = useStopAdaptationBackfill();

  const biosOn = bq.data?.bios ?? false;
  const biosRunning = bq.data?.bios_running ?? false;
  const biosMode = bq.data?.bios_mode ?? 'off';
  const adaptOn = bq.data?.adaptations ?? false;
  const adaptRunning = bq.data?.adaptations_running ?? false;
  const adaptMode = bq.data?.adaptations_mode ?? 'off';

  const [biosRpm, setBiosRpm] = useState('');
  const [adaptRpm, setAdaptRpm] = useState('');
  const baInit = useRef(false);
  useEffect(() => {
    if (bq.data && !baInit.current) {
      setBiosRpm(String(bq.data.bios_rpm));
      setAdaptRpm(String(bq.data.adaptations_rpm));
      baInit.current = true;
    }
  }, [bq.data]);

  const baNums = { bios: Number(biosRpm), adapt: Number(adaptRpm) };
  const baInvalid =
    [biosRpm, adaptRpm].some((s) => s === '') || Object.values(baNums).some((n) => Number.isNaN(n) || n < 0);
  const baDirty =
    !!bq.data && (biosRpm !== String(bq.data.bios_rpm) || adaptRpm !== String(bq.data.adaptations_rpm));

  const applyBa = async (patch: Partial<BioAdaptationInput>, msg: string) => {
    if (!bq.data) return;
    try {
      await updateBa.mutateAsync(buildBaInput(bq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };

  // ── Общий SaveBar (числовые поля всех секций) ──
  const dirty = colDirty || yearDirty || coverDirty || baDirty;
  const saveInvalid =
    (colDirty && colInvalid) ||
    (yearDirty && yearInvalid) ||
    (coverDirty && coverInvalid) ||
    (baDirty && baInvalid);
  const saving = updateCol.isPending || updateYear.isPending || updateCover.isPending || updateBa.isPending;

  const onReset = () => {
    if (cq.data) {
      setMaxMB(String(cq.data.cache_max_mb));
      setMinFreeMB(String(cq.data.cache_min_free_mb));
      setPosterMB(String(cq.data.poster_cache_max_mb));
      setPhotoMB(String(cq.data.photo_cache_max_mb));
    }
    if (yq.data) {
      setOlRpm(String(yq.data.openlibrary_rpm));
      setWdRpm(String(yq.data.wikidata_rpm));
      setNfDays(String(yq.data.not_found_retry_days));
      setErrHours(String(yq.data.error_retry_hours));
    }
    if (xq.data) {
      setOlRpmC(String(xq.data.openlibrary_rpm));
      setGbRpmC(String(xq.data.googlebooks_rpm));
      setNfDaysC(String(xq.data.not_found_retry_days));
      setErrHoursC(String(xq.data.error_retry_hours));
    }
    if (bq.data) {
      setBiosRpm(String(bq.data.bios_rpm));
      setAdaptRpm(String(bq.data.adaptations_rpm));
    }
  };

  const onSave = async () => {
    try {
      if (colDirty && !colInvalid && cq.data) {
        const saved = await updateCol.mutateAsync(
          buildCollectionInput(cq.data, {
            cache_max_mb: maxN,
            cache_min_free_mb: minFreeN,
            poster_cache_max_mb: posterN,
            photo_cache_max_mb: photoN,
          }),
        );
        setMaxMB(String(saved.cache_max_mb));
        setMinFreeMB(String(saved.cache_min_free_mb));
        setPosterMB(String(saved.poster_cache_max_mb));
        setPhotoMB(String(saved.photo_cache_max_mb));
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
      if (coverDirty && !coverInvalid && xq.data) {
        const saved = await updateCover.mutateAsync(
          buildCoverInput(xq.data, {
            openlibrary_rpm: cNums.ol,
            googlebooks_rpm: cNums.gb,
            not_found_retry_days: cNums.nf,
            error_retry_hours: cNums.eh,
          }),
        );
        setOlRpmC(String(saved.openlibrary_rpm));
        setGbRpmC(String(saved.googlebooks_rpm));
        setNfDaysC(String(saved.not_found_retry_days));
        setErrHoursC(String(saved.error_retry_hours));
      }
      if (baDirty && !baInvalid && bq.data) {
        const saved = await updateBa.mutateAsync(
          buildBaInput(bq.data, { bios_rpm: baNums.bios, adaptations_rpm: baNums.adapt }),
        );
        setBiosRpm(String(saved.bios_rpm));
        setAdaptRpm(String(saved.adaptations_rpm));
      }
      toast.success('Сохранено');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  // ── Действия секции 1 ──
  const onClear = async () => {
    if (!window.confirm('Очистить весь кэш обложек книг? Они переизвлекутся из fb2 по мере просмотра.')) return;
    try {
      const r = await clear.mutateAsync();
      toast.success(`Кэш очищен: удалено файлов — ${r.removed}`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось очистить');
    }
  };
  const onClearPosters = async () => {
    if (!window.confirm('Очистить постеры экранизаций? Вернутся при следующем дозаполнении экранизаций.')) return;
    try {
      const r = await clearPosters.mutateAsync();
      toast.success(`Постеры очищены: удалено файлов — ${r.removed}`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось очистить');
    }
  };
  const onClearPhotos = async () => {
    if (!window.confirm('Очистить фото авторов? Вернутся при следующем дозаполнении биографий.')) return;
    try {
      const r = await clearPhotos.mutateAsync();
      toast.success(`Фото очищены: удалено файлов — ${r.removed}`);
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

  // ── Действия секции 2а (годы) ──
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

  // ── Действия секции 2б (обложки) ──
  const onRunCover = async () => {
    try {
      await runCover.mutateAsync();
      toast.success('Дозаполнение запущено в фоне');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось запустить');
    }
  };
  const onStopCover = async () => {
    try {
      await stopCover.mutateAsync();
      toast.success('Останавливаю…');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось остановить');
    }
  };

  // ── Действия секции 2в (биографии + экранизации) ──
  const onRunBio = async () => {
    try {
      await runBio.mutateAsync();
      toast.success('Дозаполнение биографий запущено');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось запустить');
    }
  };
  const onStopBio = async () => {
    try {
      await stopBio.mutateAsync();
      toast.success('Останавливаю…');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось остановить');
    }
  };
  const onRunAdapt = async () => {
    try {
      await runAdapt.mutateAsync();
      toast.success('Поиск экранизаций запущен');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось запустить');
    }
  };
  const onStopAdapt = async () => {
    try {
      await stopAdapt.mutateAsync();
      toast.success('Останавливаю…');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось остановить');
    }
  };

  const yCov = yq.data?.coverage;
  const yPct = yCov && yCov.total > 0 ? Math.round((yCov.with_year / yCov.total) * 100) : 0;
  const xCov = xq.data?.coverage;
  const xPct = xCov && xCov.total > 0 ? Math.round((xCov.with_cover / xCov.total) * 100) : 0;
  const bCov = bq.data?.bio_coverage;
  const bPct = bCov && bCov.total > 0 ? Math.round((bCov.with_bio / bCov.total) * 100) : 0;
  const aCov = bq.data?.adaptation_coverage;
  const aPct = aCov && aCov.total > 0 ? Math.round((aCov.with_adaptations / aCov.total) * 100) : 0;
  const loading = cq.isLoading || yq.isLoading || xq.isLoading || bq.isLoading;
  const failed = cq.error || yq.error || xq.error || bq.error;

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
                <p className="text-sm font-medium">Кэш обложек книг</p>
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

              {/* Постеры экранизаций и фото авторов — отдельные бакеты:
                  нерегенерируемые (внешний источник), поэтому со своими лимитами
                  и очисткой, чтобы «Очистить кэш обложек»/LRU их не сносили. */}
              <div className="space-y-3 border-t border-border pt-3">
                <p className="text-sm font-medium">Постеры и фото авторов</p>
                <p className="text-xs text-muted-foreground">
                  Отдельно от обложек книг — они из внешних источников и не воссоздаются из fb2.
                  0 = без лимита (рекомендуется: объём мал, а эвикция ломала бы их).
                </p>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <Label htmlFor="poster-max">Постеры, бюджет МБ</Label>
                    <Input
                      id="poster-max"
                      type="number"
                      min={0}
                      value={posterMB}
                      onChange={(e) => setPosterMB(e.target.value)}
                    />
                    <p className="text-xs tabular-nums text-muted-foreground">
                      {formatBytes(cq.data?.poster_cache_size_bytes ?? 0)}
                    </p>
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="photo-max">Фото авторов, бюджет МБ</Label>
                    <Input
                      id="photo-max"
                      type="number"
                      min={0}
                      value={photoMB}
                      onChange={(e) => setPhotoMB(e.target.value)}
                    />
                    <p className="text-xs tabular-nums text-muted-foreground">
                      {formatBytes(cq.data?.photo_cache_size_bytes ?? 0)}
                    </p>
                  </div>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button variant="outline" size="sm" onClick={onClearPosters} disabled={clearPosters.isPending}>
                    <Trash2 className="size-4" aria-hidden />
                    {clearPosters.isPending ? 'Очистка…' : 'Очистить постеры'}
                  </Button>
                  <Button variant="outline" size="sm" onClick={onClearPhotos} disabled={clearPhotos.isPending}>
                    <Trash2 className="size-4" aria-hidden />
                    {clearPhotos.isPending ? 'Очистка…' : 'Очистить фото'}
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>

          {/* ─────────── Секция 2а: внешние источники — годы ─────────── */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Внешние источники — годы</CardTitle>
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
                  Фоновое дозаполнение года из OpenLibrary / Wikidata
                </Label>
              </div>
              <p className="text-xs text-muted-foreground">
                Тянет год для книг, у которых его нет. Ходит в публичные API, поэтому с ограничением скорости.
                Режим охвата — переключатель ниже.
              </p>

              <div className="space-y-2 border-l border-border pl-3">
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="src-ol"
                    checked={yq.data?.openlibrary ?? false}
                    disabled={updateYear.isPending}
                    onCheckedChange={(v) => void applyYear({ openlibrary: v }, 'Применено')}
                  />
                  <Label htmlFor="src-ol" className="cursor-pointer text-sm">OpenLibrary (first_publish_year)</Label>
                </div>
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="src-wd"
                    checked={yq.data?.wikidata ?? false}
                    disabled={updateYear.isPending}
                    onCheckedChange={(v) => void applyYear({ wikidata: v }, 'Применено')}
                  />
                  <Label htmlFor="src-wd" className="cursor-pointer text-sm">Wikidata (P577)</Label>
                </div>
              </div>

              <WholeCollectionSwitch
                id="year-whole"
                checked={yq.data?.whole_collection ?? false}
                disabled={updateYear.isPending}
                onChange={(v) =>
                  void applyYear({ whole_collection: v }, v ? 'Режим: вся коллекция' : 'Режим: фолбэк')
                }
                warning="Вся коллекция: год запрашивается у внешних источников и для книг, которых fb2-проход не касался. Это десятки тысяч запросов и очень долго."
              />

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
                <span className="tabular-nums">{yCov ? `${yCov.with_year} из ${yCov.total} (${yPct}%)` : '—'}</span>
              </div>
              {yCov && Object.keys(yCov.by_source).length > 0 ? (
                <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  {Object.entries(yCov.by_source)
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

          {/* ─────────── Секция 2б: внешние источники — обложки ─────────── */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Внешние источники — обложки</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 pt-2 sm:max-w-md">
              <div className="flex items-center gap-2.5">
                <Switch
                  id="cov-master"
                  checked={covEnabled}
                  disabled={updateCover.isPending}
                  onCheckedChange={(v) =>
                    void applyCover({ enabled: v }, v ? 'Фоновое дозаполнение включено' : 'Выключено')
                  }
                />
                <Label htmlFor="cov-master" className="cursor-pointer text-sm font-medium">
                  Фоновое дозаполнение обложек из OpenLibrary / Google Books
                </Label>
              </div>
              <p className="text-xs text-muted-foreground">
                Тянет обложку для книг без неё из fb2. Ходит в публичные API, поэтому с ограничением скорости;
                хит-рейт для русскоязычных книг невысокий. Режим охвата — переключатель ниже.
              </p>

              <div className="space-y-2 border-l border-border pl-3">
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="cov-src-ol"
                    checked={xq.data?.openlibrary ?? false}
                    disabled={updateCover.isPending}
                    onCheckedChange={(v) => void applyCover({ openlibrary: v }, 'Применено')}
                  />
                  <Label htmlFor="cov-src-ol" className="cursor-pointer text-sm">OpenLibrary</Label>
                </div>
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="cov-src-gb"
                    checked={xq.data?.googlebooks ?? false}
                    disabled={updateCover.isPending}
                    onCheckedChange={(v) => void applyCover({ googlebooks: v }, 'Применено')}
                  />
                  <Label htmlFor="cov-src-gb" className="cursor-pointer text-sm">Google Books</Label>
                </div>
              </div>

              <WholeCollectionSwitch
                id="cover-whole"
                checked={xq.data?.whole_collection ?? false}
                disabled={updateCover.isPending}
                onChange={(v) =>
                  void applyCover({ whole_collection: v }, v ? 'Режим: вся коллекция' : 'Режим: фолбэк')
                }
                warning="Вся коллекция: обложка запрашивается у внешних источников и для книг, которых fb2-проход не касался. Это десятки тысяч запросов к OpenLibrary/Google Books и очень долго."
              />

              <div className="grid grid-cols-2 gap-3 border-t border-border pt-3">
                <div className="space-y-1.5">
                  <Label htmlFor="cov-ol-rpm">OpenLibrary, запросов/мин</Label>
                  <Input id="cov-ol-rpm" type="number" min={0} value={olRpmC} onChange={(e) => setOlRpmC(e.target.value)} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="cov-gb-rpm">Google Books, запросов/мин</Label>
                  <Input id="cov-gb-rpm" type="number" min={0} value={gbRpmC} onChange={(e) => setGbRpmC(e.target.value)} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="cov-nf-days">Ретрай «не найдено», дней</Label>
                  <Input id="cov-nf-days" type="number" min={0} value={nfDaysC} onChange={(e) => setNfDaysC(e.target.value)} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="cov-err-hours">Ретрай ошибки, часов</Label>
                  <Input id="cov-err-hours" type="number" min={0} value={errHoursC} onChange={(e) => setErrHoursC(e.target.value)} />
                </div>
              </div>

              <div className="flex flex-wrap gap-2">
                {covMode === 'once' ? (
                  <Button variant="outline" onClick={onStopCover} disabled={stopCover.isPending}>
                    <Square className="size-4" aria-hidden />
                    {stopCover.isPending ? 'Остановка…' : 'Остановить проход'}
                  </Button>
                ) : (
                  <Button variant="outline" onClick={onRunCover} disabled={covEnabled || covRunning || runCover.isPending}>
                    <Flame className="size-4" aria-hidden />
                    {runCover.isPending ? 'Запуск…' : 'Прогнать разово'}
                  </Button>
                )}
              </div>
              <p className="text-xs text-muted-foreground">
                «Прогнать разово» — однократный проход; постоянную работу включает тумблер выше.
              </p>
              {covRunning ? (
                <p className="flex items-center gap-2 text-xs text-muted-foreground">
                  <span className="inline-block size-2 animate-pulse rounded-full bg-primary" aria-hidden />
                  {covMode === 'continuous' ? 'Непрерывный воркер активен.' : 'Идёт разовый проход…'}
                </p>
              ) : null}

              <div className="grid grid-cols-2 gap-3 border-t border-border pt-3 text-sm">
                <span className="text-muted-foreground">Обложка есть</span>
                <span className="tabular-nums">{xCov ? `${xCov.with_cover} из ${xCov.total} (${xPct}%)` : '—'}</span>
              </div>
              {xCov && Object.keys(xCov.by_source).length > 0 ? (
                <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  <span className="w-full text-muted-foreground/80">Из внешних источников добавлено:</span>
                  {Object.entries(xCov.by_source)
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

          {/* ────── Секция 2в: внешние — биографии + экранизации ────── */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Внешние источники — биографии и экранизации</CardTitle>
            </CardHeader>
            <CardContent className="space-y-5 pt-2 sm:max-w-md">
              <p className="text-xs text-muted-foreground">
                У этих данных нет fb2-источника — только внешние API. Включённый воркер проходит по всей
                коллекции (авторов / книг), у которых данных ещё нет. Ходит в публичные API, поэтому с
                ограничением скорости. Без воркера — данные тянутся лениво при открытии карточки.
              </p>

              {/* Блок «Биографии» */}
              <div className="space-y-3">
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="ba-bios"
                    checked={biosOn}
                    disabled={updateBa.isPending}
                    onCheckedChange={(v) => void applyBa({ bios: v }, v ? 'Биографии включены' : 'Выключено')}
                  />
                  <Label htmlFor="ba-bios" className="cursor-pointer text-sm font-medium">
                    Биографии и фото авторов (Wikipedia / OpenLibrary)
                  </Label>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="bios-rpm">Скорость, авторов/мин</Label>
                  <Input
                    id="bios-rpm"
                    type="number"
                    min={0}
                    value={biosRpm}
                    onChange={(e) => setBiosRpm(e.target.value)}
                  />
                </div>
                <div className="flex flex-wrap gap-2">
                  {biosMode === 'once' ? (
                    <Button variant="outline" onClick={onStopBio} disabled={stopBio.isPending}>
                      <Square className="size-4" aria-hidden />
                      {stopBio.isPending ? 'Остановка…' : 'Остановить проход'}
                    </Button>
                  ) : (
                    <Button variant="outline" onClick={onRunBio} disabled={biosOn || biosRunning || runBio.isPending}>
                      <Flame className="size-4" aria-hidden />
                      {runBio.isPending ? 'Запуск…' : 'Прогнать разово'}
                    </Button>
                  )}
                </div>
                {biosRunning ? (
                  <p className="flex items-center gap-2 text-xs text-muted-foreground">
                    <span className="inline-block size-2 animate-pulse rounded-full bg-primary" aria-hidden />
                    {biosMode === 'continuous' ? 'Непрерывный воркер активен.' : 'Идёт разовый проход…'}
                  </p>
                ) : null}
                <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-sm">
                  <span className="text-muted-foreground">Биография есть</span>
                  <span className="tabular-nums">{bCov ? `${bCov.with_bio} из ${bCov.total} (${bPct}%)` : '—'}</span>
                  <span className="text-muted-foreground">Фото есть</span>
                  <span className="tabular-nums">{bCov ? `${bCov.with_photo} из ${bCov.total}` : '—'}</span>
                </div>
              </div>

              {/* Блок «Экранизации» */}
              <div className="space-y-3 border-t border-border pt-4">
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="ba-adapt"
                    checked={adaptOn}
                    disabled={updateBa.isPending}
                    onCheckedChange={(v) => void applyBa({ adaptations: v }, v ? 'Экранизации включены' : 'Выключено')}
                  />
                  <Label htmlFor="ba-adapt" className="cursor-pointer text-sm font-medium">
                    Экранизации книг (Wikidata)
                  </Label>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="adapt-rpm">Скорость, книг/мин</Label>
                  <Input
                    id="adapt-rpm"
                    type="number"
                    min={0}
                    value={adaptRpm}
                    onChange={(e) => setAdaptRpm(e.target.value)}
                  />
                  <p className="text-xs text-muted-foreground">
                    Через SPARQL-запросы к Wikidata (тяжелее обычных API) — держите скорость ниже.
                  </p>
                </div>
                <div className="flex flex-wrap gap-2">
                  {adaptMode === 'once' ? (
                    <Button variant="outline" onClick={onStopAdapt} disabled={stopAdapt.isPending}>
                      <Square className="size-4" aria-hidden />
                      {stopAdapt.isPending ? 'Остановка…' : 'Остановить проход'}
                    </Button>
                  ) : (
                    <Button
                      variant="outline"
                      onClick={onRunAdapt}
                      disabled={adaptOn || adaptRunning || runAdapt.isPending}
                    >
                      <Flame className="size-4" aria-hidden />
                      {runAdapt.isPending ? 'Запуск…' : 'Прогнать разово'}
                    </Button>
                  )}
                </div>
                {adaptRunning ? (
                  <p className="flex items-center gap-2 text-xs text-muted-foreground">
                    <span className="inline-block size-2 animate-pulse rounded-full bg-primary" aria-hidden />
                    {adaptMode === 'continuous' ? 'Непрерывный воркер активен.' : 'Идёт разовый проход…'}
                  </p>
                ) : null}
                <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-sm">
                  <span className="text-muted-foreground">Экранизация есть</span>
                  <span className="tabular-nums">
                    {aCov ? `${aCov.with_adaptations} из ${aCov.total} (${aPct}%)` : '—'}
                  </span>
                </div>
              </div>
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
