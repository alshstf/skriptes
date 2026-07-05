import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { AlertTriangle, ChevronRight, Flame, Info, Loader2, RotateCcw, Square, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Skeleton } from '@/components/ui/skeleton';
import { AdminTabs } from '@/components/AdminTabs';
import { SaveBar } from '@/components/SaveBar';
import { cn } from '@/lib/utils';
import { formatBytes } from '@/lib/format';
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
  useExternalRatingSettings,
  useUpdateExternalRatingSettings,
  useRunExternalRating,
  useStopExternalRating,
  useResetYearLookups,
  useResetCoverLookups,
  useResetRatingLookups,
  useRenownSettings,
  useUpdateRenownSettings,
  useRunRenown,
  useStopRenown,
  useResetRenownLookups,
  useBioAdaptationSettings,
  useUpdateBioAdaptationSettings,
  useRunBioBackfill,
  useStopBioBackfill,
  useRunAdaptationBackfill,
  useStopAdaptationBackfill,
  useEnrichmentGates,
  useUpdateEnrichmentGates,
  useWorkGroupingSettings,
  useUpdateWorkGroupingSettings,
  useRunWorkGrouping,
  useStopWorkGrouping,
  useRegroupAllWorks,
  useStopWorksRegroup,
  type CoverCacheSettings,
  type CollectionInput,
  type Intensity,
  type YearEnrichmentSettings,
  type YearEnrichmentInput,
  type CoverEnrichmentSettings,
  type CoverEnrichmentInput,
  type ExternalRatingSettings,
  type ExternalRatingInput,
  type RenownSettings,
  type RenownInput,
  type BioAdaptationSettings,
  type BioAdaptationInput,
  type EnrichmentGates,
  type WorkGroupingSettings,
  type WorkGroupingInput,
} from '@/lib/admin';
import { ApiError } from '@/lib/api';

/**
 * AdminBackgroundPage — /admin/background. Управление обогащением карточек,
 * организованное ВОКРУГ ТИПОВ ДАННЫХ (обложки / аннотации / год / био+фото /
 * экранизации). На каждый тип — единый режим:
 *
 *   Выкл → Лениво (по запросу) → Фоном (заранее по всей коллекции + по запросу).
 *
 * «Режим» — производное состояние: под капотом он раскладывается на «выключатель»
 * lazy (enrichment_gates), локальные fb2-тумблеры (общий мастер prewarm) и
 * внешние воркеры. См. applyMode — там вся раскладка в одном месте.
 *
 * Год — двухпозиционный {Выкл, Фоном}: у него нет lazy-пути (нужен сразу для
 * всей коллекции — фильтр/сортировка/гистограмма).
 *
 * Числовые поля (rpm, бюджеты кэшей, порог свободного места) собираются в один
 * SaveBar; тумблеры/режимы/источники применяются сразу.
 */

type Mode = 'off' | 'lazy' | 'bg';
// Типы с локальным fb2-источником (делят общий мастер prewarm).
type LocalKind = 'cover' | 'annotation' | 'year';

const MODE_HELP: Record<Mode, string> = {
  off: 'Не загружать вообще — даже по запросу при открытии карточки.',
  lazy: 'Только по запросу при открытии карточки (первая загрузка — с задержкой).',
  bg: 'Заранее по всей коллекции. По запросу тоже работает — добирает новые и пропущенные.',
};

const MODE_HELP_YEAR: Record<'off' | 'bg', string> = {
  off: 'Не заполнять. Фильтр, сортировка и гистограмма по году работать не будут.',
  bg: 'Заранее по всей коллекции (у года нет режима «по запросу» — он нужен сразу для всех книг).',
};

const MODE_LABEL: Record<Mode, string> = { off: 'Выкл', lazy: 'Лениво', bg: 'Фоном' };

const MIN_FREE_WARN_MB = 100;

const SOURCE_LABELS: Record<string, string> = {
  fb2_title: 'из fb2',
  openlibrary: 'OpenLibrary',
  wikidata: 'Wikidata',
  googlebooks: 'Google Books',
  fantlab: 'Фантлаб',
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

function buildWgInput(d: WorkGroupingSettings, patch: Partial<WorkGroupingInput>): WorkGroupingInput {
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

function buildExtRatingInput(d: ExternalRatingSettings, patch: Partial<ExternalRatingInput>): ExternalRatingInput {
  return {
    enabled: d.enabled,
    googlebooks: d.googlebooks,
    openlibrary: d.openlibrary,
    whole_collection: d.whole_collection,
    googlebooks_rpm: d.googlebooks_rpm,
    openlibrary_rpm: d.openlibrary_rpm,
    not_found_retry_days: d.not_found_retry_days,
    error_retry_hours: d.error_retry_hours,
    ...patch,
  };
}

function buildRenownInput(d: RenownSettings, patch: Partial<RenownInput>): RenownInput {
  return {
    enabled: d.enabled,
    fantlab: d.fantlab,
    openlibrary: d.openlibrary,
    wikidata: d.wikidata,
    whole_collection: d.whole_collection,
    fantlab_rpm: d.fantlab_rpm,
    openlibrary_rpm: d.openlibrary_rpm,
    wikidata_rpm: d.wikidata_rpm,
    found_refresh_days: d.found_refresh_days,
    not_found_retry_days: d.not_found_retry_days,
    error_retry_hours: d.error_retry_hours,
    ...patch,
  };
}

const MODE_HELP_WG: Record<'off' | 'bg', string> = {
  off: 'Издания не группируются — каждый fb2-файл остаётся отдельной книгой.',
  bg: 'Слияние изданий в логические книги по всей коллекции (локально + внешние Work ID).',
};

const MODE_HELP_RATING: Record<'off' | 'bg', string> = {
  off: 'Внешний рейтинг из сети не запрашивается (остаётся только LIBRATE, если есть).',
  bg: 'Дозаполнять внешний рейтинг (Google Books / OpenLibrary) по книгам без рейтинга.',
};

const MODE_HELP_RENOWN: Record<'off' | 'bg', string> = {
  off: 'Счётчики известности из сети не запрашиваются (популярность — только из локальных сигналов).',
  bg: 'Дозаполнять счётчики известности (Фантлаб / OpenLibrary) — усиливают сортировку по популярности.',
};

// ── Презентационные примитивы аккордеона ──

function ModeBadge({ mode }: { mode: Mode }) {
  return (
    <span
      className={cn(
        'rounded-md border border-border px-2 py-0.5 text-xs',
        mode === 'off' && 'text-muted-foreground',
      )}
    >
      {MODE_LABEL[mode]}
    </span>
  );
}

function ModeSelector({
  value,
  onChange,
  twoState,
  disabled,
  idPrefix,
  help,
}: {
  value: Mode;
  onChange: (m: Mode) => void;
  twoState?: boolean;
  disabled?: boolean;
  idPrefix: string;
  help?: Record<'off' | 'bg', string>; // переопределить двухпозиционный help-текст
}) {
  const modes: Mode[] = twoState ? ['off', 'bg'] : ['off', 'lazy', 'bg'];
  return (
    <div className="space-y-1.5">
      <p className="text-xs uppercase tracking-wide text-muted-foreground">Режим</p>
      <div className="flex gap-1" role="group" aria-label="Режим">
        {modes.map((m) => (
          <Button
            key={m}
            type="button"
            size="sm"
            data-testid={`${idPrefix}-mode-${m}`}
            aria-pressed={value === m}
            variant={value === m ? 'default' : 'outline'}
            disabled={disabled}
            onClick={() => onChange(m)}
          >
            {MODE_LABEL[m]}
          </Button>
        ))}
      </div>
      <p className="text-xs text-muted-foreground text-pretty">
        {twoState ? (help ?? MODE_HELP_YEAR)[value === 'bg' ? 'bg' : 'off'] : MODE_HELP[value]}
      </p>
    </div>
  );
}

function FieldLabel({ children }: { children: React.ReactNode }) {
  return <p className="text-xs uppercase tracking-wide text-muted-foreground">{children}</p>;
}

// SourceSwitch — живой тумблер источника (применяется сразу). Switch, а не
// checkbox: в наших разделах checkbox = «отметь, потом Сохрани» (см. правило
// контролов), а источник применяется мгновенно.
function SourceSwitch({
  id,
  label,
  checked,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center gap-2.5">
      <Switch id={id} checked={checked} disabled={disabled} onCheckedChange={onChange} />
      <label htmlFor={id} className="cursor-pointer text-sm">
        {label}
      </label>
    </div>
  );
}

// ScopeControl — «Что заполнять» для внешних источников (фолбэк ↔ вся коллекция).
// Сегментированный выбор + пояснение/предупреждение под ним (бывший
// WholeCollectionSwitch, переделанный по просьбе в явный двух-вариантный выбор).
function ScopeControl({
  whole,
  disabled,
  onChange,
  warning,
  fallbackLabel = 'Только пропуски fb2',
  fallbackHint = 'Дозаполняются только книги, у которых локальный fb2-проход прошёл, но данных не дал (дешевле).',
}: {
  whole: boolean;
  disabled?: boolean;
  onChange: (whole: boolean) => void;
  warning: string;
  // Лейбл кнопки и пояснение узкого режима зависят от воркера: у обложек/года/
  // рейтинга это «пропуски fb2» (что локальный проход не заполнил), у известности —
  // «ядро коллекции» (работы с уже имеющимися сигналами известности). Дефолт —
  // fb2-семантика; известность переопределяет.
  fallbackLabel?: string;
  fallbackHint?: string;
}) {
  return (
    <div className="space-y-1.5">
      <p className="text-sm">Что заполнять</p>
      <div className="flex flex-wrap gap-1" role="group" aria-label="Что заполнять">
        <Button
          type="button"
          size="sm"
          aria-pressed={!whole}
          variant={!whole ? 'default' : 'outline'}
          disabled={disabled}
          onClick={() => onChange(false)}
        >
          {fallbackLabel}
        </Button>
        <Button
          type="button"
          size="sm"
          aria-pressed={whole}
          variant={whole ? 'default' : 'outline'}
          disabled={disabled}
          onClick={() => onChange(true)}
        >
          Всю коллекцию
        </Button>
      </div>
      {whole ? (
        <Callout icon={<AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>{warning}</Callout>
      ) : (
        <p className="text-xs text-muted-foreground text-pretty">{fallbackHint}</p>
      )}
    </div>
  );
}

// SourcesNote — пояснение про источники, когда они НЕ редактируются (режимы
// Выкл/Лениво). Вместо мёртвых, задизейбленных тумблеров — текст: в «Лениво»
// источники не настраиваются (грузим по запросу), выбор — в «Фоном».
function SourcesNote({ mode, lazyText }: { mode: Mode; lazyText: string }) {
  if (mode === 'bg') return null;
  return (
    <div className="space-y-2">
      <FieldLabel>Источники</FieldLabel>
      <p className="text-xs text-muted-foreground text-pretty">
        {mode === 'off' ? 'Тип выключен — не загружается даже по запросу.' : lazyText}
      </p>
    </div>
  );
}

function RunningDot({ continuous }: { continuous: boolean }) {
  return (
    <p className="flex items-center gap-2 text-xs text-muted-foreground">
      <span className="inline-block size-2 animate-pulse rounded-full bg-primary" aria-hidden />
      {continuous ? 'Непрерывный воркер активен.' : 'Идёт разовый проход…'}
    </p>
  );
}

function TypeRow({
  title,
  mode,
  coverage,
  defaultOpen,
  children,
}: {
  title: string;
  mode: Mode;
  coverage: string;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  return (
    <details className="group border-b border-border last:border-b-0" open={defaultOpen}>
      <summary className="flex cursor-pointer list-none items-center gap-3 px-4 py-3 hover:bg-muted/40 [&::-webkit-details-marker]:hidden">
        <ChevronRight
          className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90"
          aria-hidden
        />
        <span className="flex-1 font-medium">{title}</span>
        <ModeBadge mode={mode} />
        <span className="w-24 text-right text-xs tabular-nums text-muted-foreground">{coverage}</span>
      </summary>
      <div className="space-y-4 px-4 pb-5 pl-11 sm:max-w-xl">{children}</div>
    </details>
  );
}

export function AdminBackgroundPage() {
  // ── Запросы ──
  const cq = useCoverCacheSettings();
  const yq = useYearEnrichmentSettings();
  const xq = useCoverEnrichmentSettings();
  const rq = useExternalRatingSettings();
  const nq = useRenownSettings();
  const bq = useBioAdaptationSettings();
  const gq = useEnrichmentGates();
  const wq = useWorkGroupingSettings();

  // ── Мутации ──
  const updateCol = useUpdateCoverCacheSettings();
  const updateYear = useUpdateYearEnrichmentSettings();
  const updateCover = useUpdateCoverEnrichmentSettings();
  const updateBa = useUpdateBioAdaptationSettings();
  const updateGates = useUpdateEnrichmentGates();

  const clearCovers = useClearCoverCache();
  const clearPosters = useClearPosterCache();
  const clearPhotos = useClearPhotoCache();

  const runCol = usePrewarmCoverCache();
  const stopCol = useStopPrewarmCoverCache();
  const runYear = useRunYearBackfill();
  const stopYear = useStopYearBackfill();
  const runCover = useRunCoverBackfill();
  const stopCover = useStopCoverBackfill();
  const updateRating = useUpdateExternalRatingSettings();
  const runRating = useRunExternalRating();
  const stopRating = useStopExternalRating();
  const updateRenown = useUpdateRenownSettings();
  const runRenown = useRunRenown();
  const stopRenown = useStopRenown();
  const resetYear = useResetYearLookups();
  const resetCover = useResetCoverLookups();
  const resetRating = useResetRatingLookups();
  const resetRenown = useResetRenownLookups();
  const runBio = useRunBioBackfill();
  const stopBio = useStopBioBackfill();
  const runAdapt = useRunAdaptationBackfill();
  const stopAdapt = useStopAdaptationBackfill();
  const updateWg = useUpdateWorkGroupingSettings();
  const runWg = useRunWorkGrouping();
  const stopWg = useStopWorkGrouping();
  const stopRegroup = useStopWorksRegroup();
  const regroupAll = useRegroupAllWorks();

  // ── Числовые поля (общий SaveBar) ──
  const [minFreeMB, setMinFreeMB] = useState('');
  const [coverBudgetMB, setCoverBudgetMB] = useState('');
  const [posterMB, setPosterMB] = useState('');
  const [photoMB, setPhotoMB] = useState('');
  const colInit = useRef(false);
  useEffect(() => {
    if (cq.data && !colInit.current) {
      setMinFreeMB(String(cq.data.cache_min_free_mb));
      setCoverBudgetMB(String(cq.data.cache_max_mb));
      setPosterMB(String(cq.data.poster_cache_max_mb));
      setPhotoMB(String(cq.data.photo_cache_max_mb));
      colInit.current = true;
    }
  }, [cq.data]);

  const [olRpmY, setOlRpmY] = useState('');
  const [wdRpmY, setWdRpmY] = useState('');
  const yearInit = useRef(false);
  useEffect(() => {
    if (yq.data && !yearInit.current) {
      setOlRpmY(String(yq.data.openlibrary_rpm));
      setWdRpmY(String(yq.data.wikidata_rpm));
      yearInit.current = true;
    }
  }, [yq.data]);

  const [olRpmC, setOlRpmC] = useState('');
  const [gbRpmC, setGbRpmC] = useState('');
  const coverInit = useRef(false);
  useEffect(() => {
    if (xq.data && !coverInit.current) {
      setOlRpmC(String(xq.data.openlibrary_rpm));
      setGbRpmC(String(xq.data.googlebooks_rpm));
      coverInit.current = true;
    }
  }, [xq.data]);

  const [gbRpmR, setGbRpmR] = useState('');
  const [olRpmR, setOlRpmR] = useState('');
  const ratingInit = useRef(false);
  useEffect(() => {
    if (rq.data && !ratingInit.current) {
      setGbRpmR(String(rq.data.googlebooks_rpm));
      setOlRpmR(String(rq.data.openlibrary_rpm));
      ratingInit.current = true;
    }
  }, [rq.data]);

  const [flRpmN, setFlRpmN] = useState('');
  const [olRpmN, setOlRpmN] = useState('');
  const [wdRpmN, setWdRpmN] = useState('');
  const renownInit = useRef(false);
  useEffect(() => {
    if (nq.data && !renownInit.current) {
      setFlRpmN(String(nq.data.fantlab_rpm));
      setOlRpmN(String(nq.data.openlibrary_rpm));
      setWdRpmN(String(nq.data.wikidata_rpm));
      renownInit.current = true;
    }
  }, [nq.data]);

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

  // ── Производные режимы ──
  const gates: EnrichmentGates = gq.data ?? {
    cover_disabled: false,
    annotation_disabled: false,
    author_disabled: false,
    adaptation_disabled: false,
  };
  const master = cq.data?.prewarm ?? false;

  const coverMode: Mode = gates.cover_disabled
    ? 'off'
    : (master && (cq.data?.sync_covers ?? false)) || (xq.data?.enabled ?? false)
      ? 'bg'
      : 'lazy';
  const annotationMode: Mode = gates.annotation_disabled
    ? 'off'
    : master && (cq.data?.sync_annotations ?? false)
      ? 'bg'
      : 'lazy';
  const yearMode: Mode =
    (master && (cq.data?.sync_years ?? false)) || (yq.data?.enabled ?? false) ? 'bg' : 'off';
  const authorMode: Mode = gates.author_disabled ? 'off' : (bq.data?.bios ?? false) ? 'bg' : 'lazy';
  const adaptationMode: Mode = gates.adaptation_disabled
    ? 'off'
    : (bq.data?.adaptations ?? false)
      ? 'bg'
      : 'lazy';

  // ── Централизованная запись режима ──
  // Локальные fb2-тумблеры (обложки/аннотации/год) делят общий мастер prewarm,
  // поэтому при смене одного пересчитываем полный CollectionInput из объединения
  // желаемых состояний всех локальных типов (иначе смена одного убьёт фон другого).
  const applyMode = async (
    kind: 'cover' | 'annotation' | 'year' | 'author' | 'adaptation',
    mode: Mode,
  ) => {
    try {
      // 1) Выключатель lazy (нет у года).
      if (kind !== 'year' && gq.data) {
        const g: EnrichmentGates = { ...gq.data };
        if (kind === 'cover') g.cover_disabled = mode === 'off';
        if (kind === 'annotation') g.annotation_disabled = mode === 'off';
        if (kind === 'author') g.author_disabled = mode === 'off';
        if (kind === 'adaptation') g.adaptation_disabled = mode === 'off';
        await updateGates.mutateAsync(g);
      }
      // 2) Локальная обработка fb2 (обложки/аннотации/год) — общий мастер.
      if ((kind === 'cover' || kind === 'annotation' || kind === 'year') && cq.data) {
        const localBg = (k: LocalKind, cur: boolean) => (kind === k ? mode === 'bg' : cur);
        const covers = localBg('cover', master && cq.data.sync_covers);
        const annot = localBg('annotation', master && cq.data.sync_annotations);
        const years = localBg('year', master && cq.data.sync_years);
        await updateCol.mutateAsync(
          buildCollectionInput(cq.data, {
            sync_covers: covers,
            sync_annotations: annot,
            sync_years: years,
            prewarm: covers || annot || years,
          }),
        );
      }
      // 3) Внешние воркеры.
      if (kind === 'cover' && xq.data) {
        await updateCover.mutateAsync(buildCoverInput(xq.data, { enabled: mode === 'bg' }));
      }
      if (kind === 'year' && yq.data) {
        await updateYear.mutateAsync(buildYearInput(yq.data, { enabled: mode === 'bg' }));
      }
      if (kind === 'author' && bq.data) {
        await updateBa.mutateAsync(buildBaInput(bq.data, { bios: mode === 'bg' }));
      }
      if (kind === 'adaptation' && bq.data) {
        await updateBa.mutateAsync(buildBaInput(bq.data, { adaptations: mode === 'bg' }));
      }
      toast.success(`Режим: ${MODE_LABEL[mode]}`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить режим');
    }
  };

  // ── Группировка изданий (works) — самостоятельный воркер ──
  const wgMode: Mode = (wq.data?.enabled ?? false) ? 'bg' : 'off';
  const wgRunning = wq.data?.work_grouping_running ?? false;
  const wgOnce = wq.data?.work_grouping_mode === 'once';
  // Идёт массовый разбор работ (regroup): воркер приостановлен инструментом,
  // управление им на это время блокируем (включение встало бы в очередь).
  const wgRegrouping = wq.data?.work_regroup_running ?? false;
  const wgRegroupDone = wq.data?.work_regroup_done ?? 0;
  const wgRegroupTotal = wq.data?.work_regroup_total ?? 0;
  const wgCov = wq.data?.coverage;
  const applyWg = async (patch: Partial<WorkGroupingInput>, msg: string) => {
    if (!wq.data) return;
    try {
      await updateWg.mutateAsync(buildWgInput(wq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };
  const onRunWg = async () => {
    try {
      await runWg.mutateAsync();
      toast.success('Проход группировки запущен');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось запустить');
    }
  };
  const onStopWg = async () => {
    try {
      await stopWg.mutateAsync();
      toast.success('Останавливаю проход');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось остановить');
    }
  };
  const onStopRegroup = async () => {
    try {
      await stopRegroup.mutateAsync();
      toast.success('Отменяю разбор — обработанные авторы останутся разобранными');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось отменить разбор');
    }
  };
  const onRegroupAll = async () => {
    if (
      !window.confirm(
        'Пересобрать все группировки заново?\n\n' +
          'Все объединения изданий будут разобраны и собраны заново по текущим правилам, ' +
          'ошибочные внешние ключи очищены (их перепроверит фоновый воркер). ' +
          'Ручные объединения/разъединения тоже будут пересобраны. ' +
          'Идёт в фоне; прогресс и отмена — здесь же.',
      )
    )
      return;
    try {
      await regroupAll.mutateAsync();
      toast.success('Пересбор группировок запущен');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось запустить пересбор');
    }
  };

  // ── Внешний рейтинг (Google Books / OpenLibrary) — самостоятельный воркер ──
  const ratingMode: Mode = (rq.data?.enabled ?? false) ? 'bg' : 'off';
  const ratingRunning = rq.data?.external_rating_running ?? false;
  const ratingOnce = rq.data?.external_rating_mode === 'once';
  const rCov = rq.data?.coverage;
  const applyRating = async (patch: Partial<ExternalRatingInput>, msg: string) => {
    if (!rq.data) return;
    try {
      await updateRating.mutateAsync(buildExtRatingInput(rq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };
  const toggleRatingProvider = (which: 'googlebooks' | 'openlibrary', v: boolean) => {
    if (!rq.data) return;
    const gb = which === 'googlebooks' ? v : rq.data.googlebooks;
    const ol = which === 'openlibrary' ? v : rq.data.openlibrary;
    void applyRating({ [which]: v, enabled: gb || ol }, 'Применено');
  };

  // ── Известность (Фантлаб / OpenLibrary) — самостоятельный воркер ──
  const renownMode: Mode = (nq.data?.enabled ?? false) ? 'bg' : 'off';
  const renownRunning = nq.data?.renown_running ?? false;
  const renownOnce = nq.data?.renown_mode === 'once';
  const nCov = nq.data?.coverage;
  const applyRenown = async (patch: Partial<RenownInput>, msg: string) => {
    if (!nq.data) return;
    try {
      await updateRenown.mutateAsync(buildRenownInput(nq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };
  const toggleRenownProvider = (which: 'fantlab' | 'openlibrary' | 'wikidata', v: boolean) => {
    if (!nq.data) return;
    const fl = which === 'fantlab' ? v : nq.data.fantlab;
    const ol = which === 'openlibrary' ? v : nq.data.openlibrary;
    const wd = which === 'wikidata' ? v : nq.data.wikidata;
    void applyRenown({ [which]: v, enabled: fl || ol || wd }, 'Применено');
  };

  // ── Живое применение прочих тумблеров/настроек ──
  const applyCol = async (patch: Partial<CollectionInput>, msg: string) => {
    if (!cq.data) return;
    try {
      await updateCol.mutateAsync(buildCollectionInput(cq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };
  const applyYear = async (patch: Partial<YearEnrichmentInput>, msg: string) => {
    if (!yq.data) return;
    try {
      await updateYear.mutateAsync(buildYearInput(yq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };
  const applyCover = async (patch: Partial<CoverEnrichmentInput>, msg: string) => {
    if (!xq.data) return;
    try {
      await updateCover.mutateAsync(buildCoverInput(xq.data, patch));
      toast.success(msg);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };

  // ── Живое редактирование источников (режим «Фоном») ──
  // fb2 — локальный синк (общий мастер prewarm пересчитываем из объединения).
  // Внешние провайдеры — флаг провайдера + производный enabled = (есть хоть один).
  const setLocalFb2 = (kind: LocalKind, v: boolean) => {
    if (!cq.data) return;
    const covers = kind === 'cover' ? v : master && cq.data.sync_covers;
    const annot = kind === 'annotation' ? v : master && cq.data.sync_annotations;
    const years = kind === 'year' ? v : master && cq.data.sync_years;
    void applyCol(
      { sync_covers: covers, sync_annotations: annot, sync_years: years, prewarm: covers || annot || years },
      'Применено',
    );
  };
  const toggleCoverProvider = (which: 'openlibrary' | 'googlebooks', v: boolean) => {
    if (!xq.data) return;
    const ol = which === 'openlibrary' ? v : xq.data.openlibrary;
    const gb = which === 'googlebooks' ? v : xq.data.googlebooks;
    void applyCover({ [which]: v, enabled: ol || gb }, 'Применено');
  };
  const toggleYearProvider = (which: 'openlibrary' | 'wikidata', v: boolean) => {
    if (!yq.data) return;
    const ol = which === 'openlibrary' ? v : yq.data.openlibrary;
    const wd = which === 'wikidata' ? v : yq.data.wikidata;
    void applyYear({ [which]: v, enabled: ol || wd }, 'Применено');
  };

  // ── Действия (run/stop/clear) ──
  const action = (fn: () => Promise<unknown>, ok: string) => async () => {
    try {
      await fn();
      toast.success(ok);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось выполнить');
    }
  };
  // resetAction — подтверждение + сброс неудачных попыток (not_found/error) +
  // тост со счётчиком. Книги снова станут кандидатами на следующем проходе.
  const resetAction = (fn: () => Promise<{ reset: number }>) => async () => {
    if (
      !window.confirm(
        'Сбросить неудачные попытки (not_found/error)? Книги перепроверятся внешними источниками на следующем проходе.',
      )
    )
      return;
    try {
      const r = await fn();
      toast.success(`Сброшено попыток: ${r.reset} — перепроверятся на следующем проходе`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сбросить');
    }
  };
  const confirmThen = (msg: string, fn: () => Promise<{ removed: number }>, label: string) => async () => {
    if (!window.confirm(msg)) return;
    try {
      const r = await fn();
      toast.success(`${label}: удалено файлов — ${r.removed}`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось очистить');
    }
  };

  const onRunCol = action(() => runCol.mutateAsync(), 'Локальная обработка запущена');
  const onStopCol = action(() => stopCol.mutateAsync(), 'Останавливаю…');
  const onRunYear = action(() => runYear.mutateAsync(), 'Дозаполнение запущено');
  const onStopYear = action(() => stopYear.mutateAsync(), 'Останавливаю…');
  const onRunCover = action(() => runCover.mutateAsync(), 'Дозаполнение запущено');
  const onStopCover = action(() => stopCover.mutateAsync(), 'Останавливаю…');
  const onRunRating = action(() => runRating.mutateAsync(), 'Дозаполнение рейтинга запущено');
  const onStopRating = action(() => stopRating.mutateAsync(), 'Останавливаю…');
  const onRunRenown = action(() => runRenown.mutateAsync(), 'Дозаполнение известности запущено');
  const onStopRenown = action(() => stopRenown.mutateAsync(), 'Останавливаю…');
  const onResetYear = resetAction(() => resetYear.mutateAsync());
  const onResetCover = resetAction(() => resetCover.mutateAsync());
  const onResetRating = resetAction(() => resetRating.mutateAsync());
  const onResetRenown = resetAction(() => resetRenown.mutateAsync());
  const onRunBio = action(() => runBio.mutateAsync(), 'Дозаполнение биографий запущено');
  const onStopBio = action(() => stopBio.mutateAsync(), 'Останавливаю…');
  const onRunAdapt = action(() => runAdapt.mutateAsync(), 'Поиск экранизаций запущен');
  const onStopAdapt = action(() => stopAdapt.mutateAsync(), 'Останавливаю…');
  const onClearCovers = confirmThen(
    'Очистить весь кэш обложек книг? Они переизвлекутся из fb2 по мере просмотра.',
    () => clearCovers.mutateAsync(),
    'Кэш очищен',
  );
  const onClearPosters = confirmThen(
    'Очистить постеры экранизаций? Вернутся при следующем дозаполнении.',
    () => clearPosters.mutateAsync(),
    'Постеры очищены',
  );
  const onClearPhotos = confirmThen(
    'Очистить фото авторов? Вернутся при следующем дозаполнении биографий.',
    () => clearPhotos.mutateAsync(),
    'Фото очищены',
  );

  // ── Состояния воркеров ──
  const colRunning = cq.data?.prewarm_running ?? false;
  const colMode = cq.data?.prewarm_mode ?? 'off';
  const yearRunning = yq.data?.year_backfill_running ?? false;
  const yearOnceMode = yq.data?.year_backfill_mode ?? 'off';
  const coverRunning = xq.data?.cover_backfill_running ?? false;
  const coverOnceMode = xq.data?.cover_backfill_mode ?? 'off';
  const biosRunning = bq.data?.bios_running ?? false;
  const biosOnceMode = bq.data?.bios_mode ?? 'off';
  const adaptRunning = bq.data?.adaptations_running ?? false;
  const adaptOnceMode = bq.data?.adaptations_mode ?? 'off';

  // ── Числовые: dirty / invalid / save ──
  const num = (s: string) => Number(s);
  const badNum = (s: string) => s === '' || Number.isNaN(num(s)) || num(s) < 0;

  const colDirty =
    !!cq.data &&
    (minFreeMB !== String(cq.data.cache_min_free_mb) ||
      coverBudgetMB !== String(cq.data.cache_max_mb) ||
      posterMB !== String(cq.data.poster_cache_max_mb) ||
      photoMB !== String(cq.data.photo_cache_max_mb));
  const colInvalid = [minFreeMB, coverBudgetMB, posterMB, photoMB].some(badNum);
  const yearDirty = !!yq.data && (olRpmY !== String(yq.data.openlibrary_rpm) || wdRpmY !== String(yq.data.wikidata_rpm));
  const yearInvalid = [olRpmY, wdRpmY].some(badNum);
  const coverDirty = !!xq.data && (olRpmC !== String(xq.data.openlibrary_rpm) || gbRpmC !== String(xq.data.googlebooks_rpm));
  const coverInvalid = [olRpmC, gbRpmC].some(badNum);
  const ratingDirty = !!rq.data && (gbRpmR !== String(rq.data.googlebooks_rpm) || olRpmR !== String(rq.data.openlibrary_rpm));
  const ratingInvalid = [gbRpmR, olRpmR].some(badNum);
  const renownDirty =
    !!nq.data &&
    (flRpmN !== String(nq.data.fantlab_rpm) ||
      olRpmN !== String(nq.data.openlibrary_rpm) ||
      wdRpmN !== String(nq.data.wikidata_rpm));
  const renownInvalid = [flRpmN, olRpmN, wdRpmN].some(badNum);
  const baDirty = !!bq.data && (biosRpm !== String(bq.data.bios_rpm) || adaptRpm !== String(bq.data.adaptations_rpm));
  const baInvalid = [biosRpm, adaptRpm].some(badNum);

  const lowFloorWarn = !badNum(minFreeMB) && num(minFreeMB) < MIN_FREE_WARN_MB;

  const dirty = colDirty || yearDirty || coverDirty || ratingDirty || renownDirty || baDirty;
  const saveInvalid =
    (colDirty && colInvalid) ||
    (yearDirty && yearInvalid) ||
    (coverDirty && coverInvalid) ||
    (ratingDirty && ratingInvalid) ||
    (renownDirty && renownInvalid) ||
    (baDirty && baInvalid);
  const saving =
    updateCol.isPending || updateYear.isPending || updateCover.isPending || updateRating.isPending ||
    updateRenown.isPending || updateBa.isPending;

  const onReset = () => {
    if (cq.data) {
      setMinFreeMB(String(cq.data.cache_min_free_mb));
      setCoverBudgetMB(String(cq.data.cache_max_mb));
      setPosterMB(String(cq.data.poster_cache_max_mb));
      setPhotoMB(String(cq.data.photo_cache_max_mb));
    }
    if (yq.data) {
      setOlRpmY(String(yq.data.openlibrary_rpm));
      setWdRpmY(String(yq.data.wikidata_rpm));
    }
    if (xq.data) {
      setOlRpmC(String(xq.data.openlibrary_rpm));
      setGbRpmC(String(xq.data.googlebooks_rpm));
    }
    if (rq.data) {
      setGbRpmR(String(rq.data.googlebooks_rpm));
      setOlRpmR(String(rq.data.openlibrary_rpm));
    }
    if (nq.data) {
      setFlRpmN(String(nq.data.fantlab_rpm));
      setOlRpmN(String(nq.data.openlibrary_rpm));
      setWdRpmN(String(nq.data.wikidata_rpm));
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
            cache_min_free_mb: num(minFreeMB),
            cache_max_mb: num(coverBudgetMB),
            poster_cache_max_mb: num(posterMB),
            photo_cache_max_mb: num(photoMB),
          }),
        );
        setMinFreeMB(String(saved.cache_min_free_mb));
        setCoverBudgetMB(String(saved.cache_max_mb));
        setPosterMB(String(saved.poster_cache_max_mb));
        setPhotoMB(String(saved.photo_cache_max_mb));
      }
      if (yearDirty && !yearInvalid && yq.data) {
        const saved = await updateYear.mutateAsync(
          buildYearInput(yq.data, { openlibrary_rpm: num(olRpmY), wikidata_rpm: num(wdRpmY) }),
        );
        setOlRpmY(String(saved.openlibrary_rpm));
        setWdRpmY(String(saved.wikidata_rpm));
      }
      if (coverDirty && !coverInvalid && xq.data) {
        const saved = await updateCover.mutateAsync(
          buildCoverInput(xq.data, { openlibrary_rpm: num(olRpmC), googlebooks_rpm: num(gbRpmC) }),
        );
        setOlRpmC(String(saved.openlibrary_rpm));
        setGbRpmC(String(saved.googlebooks_rpm));
      }
      if (ratingDirty && !ratingInvalid && rq.data) {
        const saved = await updateRating.mutateAsync(
          buildExtRatingInput(rq.data, { googlebooks_rpm: num(gbRpmR), openlibrary_rpm: num(olRpmR) }),
        );
        setGbRpmR(String(saved.googlebooks_rpm));
        setOlRpmR(String(saved.openlibrary_rpm));
      }
      if (renownDirty && !renownInvalid && nq.data) {
        const saved = await updateRenown.mutateAsync(
          buildRenownInput(nq.data, {
            fantlab_rpm: num(flRpmN),
            openlibrary_rpm: num(olRpmN),
            wikidata_rpm: num(wdRpmN),
          }),
        );
        setFlRpmN(String(saved.fantlab_rpm));
        setOlRpmN(String(saved.openlibrary_rpm));
        setWdRpmN(String(saved.wikidata_rpm));
      }
      if (baDirty && !baInvalid && bq.data) {
        const saved = await updateBa.mutateAsync(
          buildBaInput(bq.data, { bios_rpm: num(biosRpm), adaptations_rpm: num(adaptRpm) }),
        );
        setBiosRpm(String(saved.bios_rpm));
        setAdaptRpm(String(saved.adaptations_rpm));
      }
      toast.success('Сохранено');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  // ── Покрытия ──
  const yCov = yq.data?.coverage;
  const yPct = yCov && yCov.total > 0 ? Math.round((yCov.with_year / yCov.total) * 100) : 0;
  const xCov = xq.data?.coverage;
  const xPct = xCov && xCov.total > 0 ? Math.round((xCov.with_cover / xCov.total) * 100) : 0;
  const rPct = rCov && rCov.total > 0 ? Math.round((rCov.with_rating / rCov.total) * 100) : 0;
  const bCov = bq.data?.bio_coverage;
  const bPct = bCov && bCov.total > 0 ? Math.round((bCov.with_bio / bCov.total) * 100) : 0;
  const aCov = bq.data?.adaptation_coverage;
  const aPct = aCov && aCov.total > 0 ? Math.round((aCov.with_adaptations / aCov.total) * 100) : 0;

  // rq (внешний рейтинг) и wq (группировка) — вторичные самостоятельные секции:
  // их загрузка/ошибка не блокирует страницу (рендерятся по rq.data?./wq.data?.).
  const loading = cq.isLoading || yq.isLoading || xq.isLoading || bq.isLoading || gq.isLoading;
  const failed = cq.error || yq.error || xq.error || bq.error || gq.error;

  const pendingMode = updateGates.isPending || updateCol.isPending || updateCover.isPending || updateYear.isPending || updateBa.isPending;

  return (
    <article className="space-y-5 text-pretty">
      <AdminTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Фоновые операции</h1>
        <p className="text-sm text-muted-foreground">
          Обогащение карточек данными из fb2 и внешних источников. На каждый тип — нужен ли он, как
          наполнять и за чей счёт (диск/сеть).
        </p>
      </header>

      {loading ? (
        <Skeleton className="h-64 w-full" />
      ) : failed ? (
        <p className="text-sm text-destructive">Не удалось загрузить настройки.</p>
      ) : (
        <>
          {/* ─────────── Общие ─────────── */}
          <section className="rounded-lg border border-border bg-card p-4">
            <h2 className="mb-3 text-base font-medium">Общие</h2>
            <div className="space-y-4 sm:max-w-md">
              <div className="space-y-1.5">
                <FieldLabel>Интенсивность обработки (нагрузка на диск)</FieldLabel>
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
                <p className="text-xs text-muted-foreground text-pretty">
                  Ниже — медленнее, но щадит диск (для HDD); выше — быстрее (для NVMe).
                </p>
              </div>

              <div className="grid grid-cols-2 items-end gap-3">
                <div className="space-y-1.5">
                  <label htmlFor="min-free" className="text-sm">
                    Порог свободного места, МБ
                  </label>
                  <Input
                    id="min-free"
                    type="number"
                    min={0}
                    value={minFreeMB}
                    onChange={(e) => setMinFreeMB(e.target.value)}
                  />
                </div>
                <div className="pb-1 text-sm">
                  <div className="text-muted-foreground">Свободно на диске</div>
                  <div className="tabular-nums">{formatBytes(cq.data?.free_bytes ?? -1)}</div>
                </div>
              </div>
              {lowFloorWarn ? (
                <Callout icon={<AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
                  Безопаснее держать ≥ {MIN_FREE_WARN_MB} МБ: слишком низкий порог повышает риск забить диск.
                </Callout>
              ) : null}

              {/* Локальная обработка fb2 — collection-wide разовый проход (общий
                  для обложек/аннотаций/года: они извлекаются из fb2 одним проходом). */}
              <div className="space-y-2 border-t border-border pt-3">
                <FieldLabel>Локальная обработка (fb2)</FieldLabel>
                <p className="text-xs text-muted-foreground text-pretty">
                  Однократный проход по всем включённым локально типам (обложки/аннотации/год). Постоянную
                  обработку включает режим «Фоном» у соответствующего типа.
                </p>
                <div className="flex flex-wrap gap-2">
                  {colMode === 'once' ? (
                    <Button variant="outline" size="sm" onClick={onStopCol} disabled={stopCol.isPending}>
                      <Square className="size-4" aria-hidden />
                      {stopCol.isPending ? 'Остановка…' : 'Остановить проход'}
                    </Button>
                  ) : (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={onRunCol}
                      disabled={colRunning || runCol.isPending}
                    >
                      <Flame className="size-4" aria-hidden />
                      {runCol.isPending ? 'Запуск…' : 'Прогнать разово'}
                    </Button>
                  )}
                </div>
                {colRunning ? <RunningDot continuous={colMode === 'continuous'} /> : null}
              </div>
            </div>
          </section>

          {/* ─────────── Типы данных ─────────── */}
          <section className="rounded-lg border border-border bg-card">
            <div className="border-b border-border px-4 py-3">
              <h2 className="text-base font-medium">Типы данных</h2>
              <p className="text-xs text-muted-foreground text-pretty">
                Чем выше режим — тем больше усилий: <b className="text-foreground">Выкл</b> →{' '}
                <b className="text-foreground">Лениво</b> (по запросу) → <b className="text-foreground">Фоном</b>{' '}
                (заранее + по запросу).
              </p>
            </div>

            {/* ОБЛОЖКИ */}
            <TypeRow title="Обложки" mode={coverMode} coverage={xCov ? `обложка у ${xPct}%` : '—'} defaultOpen>
              <ModeSelector
                idPrefix="cover"
                value={coverMode}
                disabled={pendingMode}
                onChange={(m) => void applyMode('cover', m)}
              />
              <SourcesNote
                mode={coverMode}
                lazyText="Лениво: обложка извлекается из fb2 по запросу при открытии. Чтобы выбрать источники и наполнять заранее (в т.ч. внешние — OpenLibrary, Google Books) — переключите в «Фоном»."
              />
              {coverMode === 'bg' ? (
                <div className="space-y-2">
                  <FieldLabel>Источники</FieldLabel>
                  <SourceSwitch
                    id="cover-src-fb2"
                    label="fb2 (локально, без сети)"
                    checked={master && (cq.data?.sync_covers ?? false)}
                    disabled={updateCol.isPending}
                    onChange={(v) => setLocalFb2('cover', v)}
                  />
                  <SourceSwitch
                    id="cover-src-ol"
                    label="OpenLibrary"
                    checked={xq.data?.openlibrary ?? false}
                    disabled={updateCover.isPending}
                    onChange={(v) => toggleCoverProvider('openlibrary', v)}
                  />
                  <SourceSwitch
                    id="cover-src-gb"
                    label="Google Books"
                    checked={xq.data?.googlebooks ?? false}
                    disabled={updateCover.isPending}
                    onChange={(v) => toggleCoverProvider('googlebooks', v)}
                  />
                  <p className="text-xs text-muted-foreground text-pretty">
                    Хотя бы один источник; если выключить все — тип вернётся в «Лениво».
                  </p>
                </div>
              ) : null}
              {coverMode === 'bg' && (xq.data?.openlibrary || xq.data?.googlebooks) ? (
                <div className="space-y-3 border-t border-border pt-3">
                  <FieldLabel>Внешние источники (OpenLibrary, Google Books)</FieldLabel>
                  <ScopeControl
                    whole={xq.data?.whole_collection ?? false}
                    disabled={updateCover.isPending}
                    onChange={(v) => void applyCover({ whole_collection: v }, v ? 'Режим: вся коллекция' : 'Режим: фолбэк')}
                    warning="Вся коллекция: обложка запрашивается у внешних источников и для книг, которых fb2-проход не касался. Десятки тысяч запросов, очень долго."
                  />
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1.5">
                      <label htmlFor="cover-ol-rpm" className="text-sm">
                        OpenLibrary, зап./мин
                      </label>
                      <Input id="cover-ol-rpm" type="number" min={0} value={olRpmC} onChange={(e) => setOlRpmC(e.target.value)} />
                    </div>
                    <div className="space-y-1.5">
                      <label htmlFor="cover-gb-rpm" className="text-sm">
                        Google Books, зап./мин
                      </label>
                      <Input id="cover-gb-rpm" type="number" min={0} value={gbRpmC} onChange={(e) => setGbRpmC(e.target.value)} />
                    </div>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {coverOnceMode === 'once' ? (
                      <Button variant="outline" size="sm" onClick={onStopCover} disabled={stopCover.isPending}>
                        <Square className="size-4" aria-hidden />
                        {stopCover.isPending ? 'Остановка…' : 'Остановить проход'}
                      </Button>
                    ) : (
                      <Button variant="outline" size="sm" onClick={onRunCover} disabled={coverRunning || runCover.isPending}>
                        <Flame className="size-4" aria-hidden />
                        {runCover.isPending ? 'Запуск…' : 'Прогнать внешние разово'}
                      </Button>
                    )}
                    <Button variant="outline" size="sm" onClick={onResetCover} disabled={resetCover.isPending}>
                      <RotateCcw className="size-4" aria-hidden />
                      {resetCover.isPending ? 'Сброс…' : 'Сбросить неудачные'}
                    </Button>
                  </div>
                  {coverRunning ? <RunningDot continuous={coverOnceMode === 'continuous'} /> : null}
                </div>
              ) : null}
              <div className="space-y-2 border-t border-border pt-3">
                <FieldLabel>Кэш обложек (тяжёлый контент)</FieldLabel>
                <div className="grid grid-cols-2 items-end gap-3">
                  <div className="space-y-1.5">
                    <label htmlFor="cover-budget" className="text-sm">
                      Бюджет, МБ (0 = без лимита)
                    </label>
                    <Input
                      id="cover-budget"
                      type="number"
                      min={0}
                      value={coverBudgetMB}
                      disabled={master}
                      onChange={(e) => setCoverBudgetMB(e.target.value)}
                    />
                  </div>
                  <div className="pb-1 text-sm">
                    <div className="text-muted-foreground">Размер кэша</div>
                    <div className="tabular-nums">{formatBytes(cq.data?.cache_size_bytes ?? 0)}</div>
                  </div>
                </div>
                {master ? (
                  <Callout icon={<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
                    Бюджет не применяется при включённой фоновой обработке — рост кэша ограничивает только порог
                    свободного места.
                  </Callout>
                ) : (
                  <p className="text-xs text-muted-foreground text-pretty">
                    При превышении вытесняются давно не запрашивавшиеся (LRU).
                  </p>
                )}
              </div>
              <div className="flex flex-wrap gap-2">
                <Button variant="outline" size="sm" onClick={onClearCovers} disabled={clearCovers.isPending}>
                  <Trash2 className="size-4" aria-hidden />
                  {clearCovers.isPending ? 'Очистка…' : 'Очистить кэш'}
                </Button>
              </div>
              {xCov?.by_source && Object.keys(xCov.by_source).length > 0 ? (
                <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  <span className="w-full text-muted-foreground/80">Из внешних добавлено:</span>
                  {Object.entries(xCov.by_source)
                    .sort((a, b) => b[1] - a[1])
                    .map(([src, n]) => (
                      <span key={src} className="tabular-nums">
                        {SOURCE_LABELS[src] ?? src}: {n}
                      </span>
                    ))}
                </div>
              ) : null}
            </TypeRow>

            {/* АННОТАЦИИ */}
            <TypeRow title="Аннотации" mode={annotationMode} coverage="—">
              <ModeSelector
                idPrefix="annotation"
                value={annotationMode}
                disabled={pendingMode}
                onChange={(m) => void applyMode('annotation', m)}
              />
              <SourcesNote
                mode={annotationMode}
                lazyText="Лениво: аннотация берётся из fb2 по запросу при открытии. Внешнего источника для аннотаций нет."
              />
              {annotationMode === 'bg' ? (
                <div className="space-y-2">
                  <FieldLabel>Источники</FieldLabel>
                  <p className="text-xs text-muted-foreground text-pretty">
                    Только fb2 (единственный источник) — извлекается локальной обработкой по всей коллекции.
                  </p>
                </div>
              ) : null}
            </TypeRow>

            {/* ГОД */}
            <TypeRow title="Год написания" mode={yearMode} coverage={yCov ? `год у ${yPct}%` : '—'} defaultOpen>
              <ModeSelector
                idPrefix="year"
                value={yearMode}
                twoState
                disabled={pendingMode}
                onChange={(m) => void applyMode('year', m)}
              />
              {yearMode === 'bg' ? (
                <>
                  <div className="space-y-2">
                    <FieldLabel>Источники</FieldLabel>
                    <SourceSwitch
                      id="year-src-fb2"
                      label="fb2 <date> (локально, без сети)"
                      checked={master && (cq.data?.sync_years ?? false)}
                      disabled={updateCol.isPending}
                      onChange={(v) => setLocalFb2('year', v)}
                    />
                    <SourceSwitch
                      id="year-src-ol"
                      label="OpenLibrary (first_publish_year)"
                      checked={yq.data?.openlibrary ?? false}
                      disabled={updateYear.isPending}
                      onChange={(v) => toggleYearProvider('openlibrary', v)}
                    />
                    <SourceSwitch
                      id="year-src-wd"
                      label="Wikidata (P577)"
                      checked={yq.data?.wikidata ?? false}
                      disabled={updateYear.isPending}
                      onChange={(v) => toggleYearProvider('wikidata', v)}
                    />
                    <p className="text-xs text-muted-foreground text-pretty">
                      Хотя бы один источник; если выключить все — год наполняться не будет.
                    </p>
                  </div>
                  {yq.data?.openlibrary || yq.data?.wikidata ? (
                    <div className="space-y-3 border-t border-border pt-3">
                      <FieldLabel>Внешние источники (OpenLibrary, Wikidata)</FieldLabel>
                      <ScopeControl
                        whole={yq.data?.whole_collection ?? false}
                        disabled={updateYear.isPending}
                        onChange={(v) => void applyYear({ whole_collection: v }, v ? 'Режим: вся коллекция' : 'Режим: фолбэк')}
                        warning="Вся коллекция: год запрашивается у внешних источников и для книг, которых fb2-проход не касался. Десятки тысяч запросов, очень долго."
                      />
                      <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1.5">
                          <label htmlFor="year-ol-rpm" className="text-sm">
                            OpenLibrary, зап./мин
                          </label>
                          <Input id="year-ol-rpm" type="number" min={0} value={olRpmY} onChange={(e) => setOlRpmY(e.target.value)} />
                        </div>
                        <div className="space-y-1.5">
                          <label htmlFor="year-wd-rpm" className="text-sm">
                            Wikidata, зап./мин
                          </label>
                          <Input id="year-wd-rpm" type="number" min={0} value={wdRpmY} onChange={(e) => setWdRpmY(e.target.value)} />
                        </div>
                      </div>
                      <div className="flex flex-wrap gap-2">
                        {yearOnceMode === 'once' ? (
                          <Button variant="outline" size="sm" onClick={onStopYear} disabled={stopYear.isPending}>
                            <Square className="size-4" aria-hidden />
                            {stopYear.isPending ? 'Остановка…' : 'Остановить проход'}
                          </Button>
                        ) : (
                          <Button variant="outline" size="sm" onClick={onRunYear} disabled={yearRunning || runYear.isPending}>
                            <Flame className="size-4" aria-hidden />
                            {runYear.isPending ? 'Запуск…' : 'Прогнать внешние разово'}
                          </Button>
                        )}
                        <Button variant="outline" size="sm" onClick={onResetYear} disabled={resetYear.isPending}>
                          <RotateCcw className="size-4" aria-hidden />
                          {resetYear.isPending ? 'Сброс…' : 'Сбросить неудачные'}
                        </Button>
                      </div>
                      {yearRunning ? <RunningDot continuous={yearOnceMode === 'continuous'} /> : null}
                    </div>
                  ) : null}
                </>
              ) : (
                <div className="space-y-2">
                  <FieldLabel>Источники</FieldLabel>
                  <p className="text-xs text-muted-foreground text-pretty">
                    Год не заполняется. Источники (fb2, OpenLibrary, Wikidata) выбираются в режиме «Фоном».
                  </p>
                </div>
              )}
              {yCov?.by_source && Object.keys(yCov.by_source).length > 0 ? (
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
            </TypeRow>

            {/* БИО + ФОТО */}
            <TypeRow title="Биографии и фото авторов" mode={authorMode} coverage={bCov ? `био у ${bPct}%` : '—'}>
              <ModeSelector
                idPrefix="author"
                value={authorMode}
                disabled={pendingMode}
                onChange={(m) => void applyMode('author', m)}
              />
              <SourcesNote
                mode={authorMode}
                lazyText="Лениво: био и фото подгружаются при открытии карточки автора (Wikipedia / OpenLibrary). Фоновый обход всех авторов — в режиме «Фоном»."
              />
              {authorMode === 'bg' ? (
                <div className="space-y-2">
                  <FieldLabel>Источники (только внешние)</FieldLabel>
                  <p className="text-xs text-muted-foreground text-pretty">
                    Wikipedia, OpenLibrary (источники фиксированы). Био и фото обогащаются вместе.
                  </p>
                </div>
              ) : null}
              {authorMode === 'bg' ? (
                <div className="space-y-3 border-t border-border pt-3">
                  <div className="space-y-1.5">
                    <label htmlFor="bios-rpm" className="text-sm">
                      Скорость, авторов/мин
                    </label>
                    <Input id="bios-rpm" type="number" min={0} className="sm:max-w-40" value={biosRpm} onChange={(e) => setBiosRpm(e.target.value)} />
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {biosOnceMode === 'once' ? (
                      <Button variant="outline" size="sm" onClick={onStopBio} disabled={stopBio.isPending}>
                        <Square className="size-4" aria-hidden />
                        {stopBio.isPending ? 'Остановка…' : 'Остановить проход'}
                      </Button>
                    ) : (
                      <Button variant="outline" size="sm" onClick={onRunBio} disabled={biosRunning || runBio.isPending}>
                        <Flame className="size-4" aria-hidden />
                        {runBio.isPending ? 'Запуск…' : 'Прогнать разово'}
                      </Button>
                    )}
                  </div>
                  {biosRunning ? <RunningDot continuous={biosOnceMode === 'continuous'} /> : null}
                </div>
              ) : null}
              <div className="space-y-2 border-t border-border pt-3">
                <FieldLabel>Кэш фото авторов</FieldLabel>
                <div className="flex flex-wrap items-end gap-3">
                  <div className="space-y-1.5">
                    <label htmlFor="photo-budget" className="text-sm">
                      Бюджет, МБ (0 = без лимита)
                    </label>
                    <Input
                      id="photo-budget"
                      type="number"
                      min={0}
                      className="sm:max-w-40"
                      value={photoMB}
                      onChange={(e) => setPhotoMB(e.target.value)}
                    />
                  </div>
                  <Button variant="outline" size="sm" onClick={onClearPhotos} disabled={clearPhotos.isPending}>
                    <Trash2 className="size-4" aria-hidden />
                    {clearPhotos.isPending ? 'Очистка…' : 'Очистить фото'}
                  </Button>
                </div>
                <p className="text-xs tabular-nums text-muted-foreground">
                  Сейчас: {formatBytes(cq.data?.photo_cache_size_bytes ?? 0)}
                </p>
              </div>
            </TypeRow>

            {/* ЭКРАНИЗАЦИИ */}
            <TypeRow title="Экранизации" mode={adaptationMode} coverage={aCov ? `есть у ${aPct}%` : '—'}>
              <ModeSelector
                idPrefix="adaptation"
                value={adaptationMode}
                disabled={pendingMode}
                onChange={(m) => void applyMode('adaptation', m)}
              />
              <SourcesNote
                mode={adaptationMode}
                lazyText="Лениво: экранизации ищутся при открытии карточки книги (Wikidata). Фоновый обход всей коллекции — в режиме «Фоном»."
              />
              {adaptationMode === 'bg' ? (
                <div className="space-y-2">
                  <FieldLabel>Источники (только внешние)</FieldLabel>
                  <p className="text-xs text-muted-foreground text-pretty">Wikidata (SPARQL) — источник фиксирован.</p>
                </div>
              ) : null}
              {adaptationMode === 'bg' ? (
                <div className="space-y-3 border-t border-border pt-3">
                  <div className="space-y-1.5">
                    <label htmlFor="adapt-rpm" className="text-sm">
                      Скорость, книг/мин
                    </label>
                    <Input id="adapt-rpm" type="number" min={0} className="sm:max-w-40" value={adaptRpm} onChange={(e) => setAdaptRpm(e.target.value)} />
                    <p className="text-xs text-muted-foreground text-pretty">
                      Через SPARQL к Wikidata (тяжелее обычных API) — держите скорость ниже.
                    </p>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {adaptOnceMode === 'once' ? (
                      <Button variant="outline" size="sm" onClick={onStopAdapt} disabled={stopAdapt.isPending}>
                        <Square className="size-4" aria-hidden />
                        {stopAdapt.isPending ? 'Остановка…' : 'Остановить проход'}
                      </Button>
                    ) : (
                      <Button variant="outline" size="sm" onClick={onRunAdapt} disabled={adaptRunning || runAdapt.isPending}>
                        <Flame className="size-4" aria-hidden />
                        {runAdapt.isPending ? 'Запуск…' : 'Прогнать разово'}
                      </Button>
                    )}
                  </div>
                  {adaptRunning ? <RunningDot continuous={adaptOnceMode === 'continuous'} /> : null}
                </div>
              ) : null}
              <div className="space-y-2 border-t border-border pt-3">
                <FieldLabel>Кэш постеров</FieldLabel>
                <div className="flex flex-wrap items-end gap-3">
                  <div className="space-y-1.5">
                    <label htmlFor="poster-budget" className="text-sm">
                      Бюджет, МБ (0 = без лимита)
                    </label>
                    <Input
                      id="poster-budget"
                      type="number"
                      min={0}
                      className="sm:max-w-40"
                      value={posterMB}
                      onChange={(e) => setPosterMB(e.target.value)}
                    />
                  </div>
                  <Button variant="outline" size="sm" onClick={onClearPosters} disabled={clearPosters.isPending}>
                    <Trash2 className="size-4" aria-hidden />
                    {clearPosters.isPending ? 'Очистка…' : 'Очистить постеры'}
                  </Button>
                </div>
                <p className="text-xs tabular-nums text-muted-foreground">
                  Сейчас: {formatBytes(cq.data?.poster_cache_size_bytes ?? 0)}
                </p>
              </div>
            </TypeRow>

            {/* ВНЕШНИЙ РЕЙТИНГ */}
            <TypeRow
              title="Внешний рейтинг"
              mode={ratingMode}
              coverage={rCov ? `рейтинг у ${rPct}%` : '—'}
            >
              <ModeSelector
                idPrefix="rating"
                value={ratingMode}
                twoState
                help={MODE_HELP_RATING}
                disabled={updateRating.isPending}
                onChange={(m) => void applyRating({ enabled: m === 'bg' }, `Режим: ${MODE_LABEL[m]}`)}
              />
              <Callout icon={<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
                Рейтинг из сети (Google Books / OpenLibrary) для книг без рейтинга. На карточке
                показывается как «Внешний рейтинг», когда нет библиотечного (LIBRATE из INPX).
                Оценки читателей этого инстанса — отдельная сущность, сюда не входят.
              </Callout>
              {ratingMode === 'bg' ? (
                <>
                  <div className="space-y-2">
                    <FieldLabel>Источники (только внешние)</FieldLabel>
                    <SourceSwitch
                      id="rating-src-gb"
                      label="Google Books (averageRating)"
                      checked={rq.data?.googlebooks ?? false}
                      disabled={updateRating.isPending}
                      onChange={(v) => toggleRatingProvider('googlebooks', v)}
                    />
                    <SourceSwitch
                      id="rating-src-ol"
                      label="OpenLibrary (ratings)"
                      checked={rq.data?.openlibrary ?? false}
                      disabled={updateRating.isPending}
                      onChange={(v) => toggleRatingProvider('openlibrary', v)}
                    />
                    <p className="text-xs text-muted-foreground text-pretty">
                      Хотя бы один источник; если выключить все — рейтинг наполняться не будет. Из
                      включённых берётся оценка с бОльшим числом голосов.
                    </p>
                  </div>
                  {rq.data?.googlebooks || rq.data?.openlibrary ? (
                    <div className="space-y-3 border-t border-border pt-3">
                      <ScopeControl
                        whole={rq.data?.whole_collection ?? false}
                        disabled={updateRating.isPending}
                        onChange={(v) => void applyRating({ whole_collection: v }, v ? 'Режим: вся коллекция' : 'Режим: только пробелы')}
                        warning="Вся коллекция: рейтинг запрашивается и для книг с библиотечным рейтингом (LIBRATE). На показ LIBRATE приоритетнее, web-данные просто накопятся. Десятки тысяч запросов, очень долго."
                      />
                      <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1.5">
                          <label htmlFor="rating-gb-rpm" className="text-sm">
                            Google Books, зап./мин
                          </label>
                          <Input id="rating-gb-rpm" type="number" min={0} value={gbRpmR} onChange={(e) => setGbRpmR(e.target.value)} />
                        </div>
                        <div className="space-y-1.5">
                          <label htmlFor="rating-ol-rpm" className="text-sm">
                            OpenLibrary, зап./мин
                          </label>
                          <Input id="rating-ol-rpm" type="number" min={0} value={olRpmR} onChange={(e) => setOlRpmR(e.target.value)} />
                        </div>
                      </div>
                      <div className="flex flex-wrap gap-2">
                        {ratingOnce ? (
                          <Button variant="outline" size="sm" onClick={onStopRating} disabled={stopRating.isPending}>
                            <Square className="size-4" aria-hidden />
                            {stopRating.isPending ? 'Остановка…' : 'Остановить проход'}
                          </Button>
                        ) : (
                          <Button variant="outline" size="sm" onClick={onRunRating} disabled={ratingRunning || runRating.isPending}>
                            <Flame className="size-4" aria-hidden />
                            {runRating.isPending ? 'Запуск…' : 'Прогнать разово'}
                          </Button>
                        )}
                        <Button variant="outline" size="sm" onClick={onResetRating} disabled={resetRating.isPending}>
                          <RotateCcw className="size-4" aria-hidden />
                          {resetRating.isPending ? 'Сброс…' : 'Сбросить неудачные'}
                        </Button>
                      </div>
                      {ratingRunning ? <RunningDot continuous={!ratingOnce} /> : null}
                    </div>
                  ) : null}
                </>
              ) : null}
              {rCov?.by_source && Object.keys(rCov.by_source).length > 0 ? (
                <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  <span className="w-full text-muted-foreground/80">Из внешних добавлено:</span>
                  {Object.entries(rCov.by_source)
                    .sort((a, b) => b[1] - a[1])
                    .map(([src, n]) => (
                      <span key={src} className="tabular-nums">
                        {SOURCE_LABELS[src] ?? src}: {n}
                      </span>
                    ))}
                </div>
              ) : null}
            </TypeRow>

            {/* ИЗВЕСТНОСТЬ (счётчики Фантлаб/OL → популярность работ) */}
            <TypeRow
              title="Известность"
              mode={renownMode}
              coverage={
                nCov
                  ? // Знаменатель — вселенная текущего охвата: «вся коллекция» → все
                    // работы, иначе ядро. max с числителем защищает от инверсии
                    // (счётчики вне ядра остаются, если раньше гоняли всю коллекцию).
                    `${nCov.with_any} из ${Math.max(
                      nq.data?.whole_collection ? nCov.total : nCov.head_total,
                      nCov.with_any,
                    )}`
                  : '—'
              }
            >
              <ModeSelector
                idPrefix="renown"
                value={renownMode}
                twoState
                help={MODE_HELP_RENOWN}
                disabled={updateRenown.isPending}
                onChange={(m) => void applyRenown({ enabled: m === 'bg' }, `Режим: ${MODE_LABEL[m]}`)}
              />
              <Callout icon={<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
                Счётчики известности книги в мире (число оценок на Фантлабе, оценки и полка
                «хочу прочитать» Open Library) — усиливают сортировку по популярности и порядок
                каталога. По умолчанию обходится ядро коллекции: работы с переизданиями,
                экранизацией или рейтингом LIBRATE.
              </Callout>
              {renownMode === 'bg' ? (
                <>
                  <div className="space-y-2">
                    <FieldLabel>Источники (только внешние)</FieldLabel>
                    <SourceSwitch
                      id="renown-src-fl"
                      label="Фантлаб (число оценок)"
                      checked={nq.data?.fantlab ?? false}
                      disabled={updateRenown.isPending}
                      onChange={(v) => toggleRenownProvider('fantlab', v)}
                    />
                    <SourceSwitch
                      id="renown-src-ol"
                      label="OpenLibrary (оценки + want-to-read)"
                      checked={nq.data?.openlibrary ?? false}
                      disabled={updateRenown.isPending}
                      onChange={(v) => toggleRenownProvider('openlibrary', v)}
                    />
                    <SourceSwitch
                      id="renown-src-wd"
                      label="Wikidata (число языковых разделов Википедии)"
                      checked={nq.data?.wikidata ?? false}
                      disabled={updateRenown.isPending}
                      onChange={(v) => toggleRenownProvider('wikidata', v)}
                    />
                    <p className="text-xs text-muted-foreground text-pretty">
                      Фантлаб силён на русскоязычной фантастике (нативный русский поиск),
                      OpenLibrary — на переводной мировой литературе (поиск по оригиналу),
                      Wikidata — на классике и мейнстриме (сколько Википедий пишут о книге).
                    </p>
                  </div>
                  {nq.data?.fantlab || nq.data?.openlibrary || nq.data?.wikidata ? (
                    <div className="space-y-3 border-t border-border pt-3">
                      <ScopeControl
                        whole={nq.data?.whole_collection ?? false}
                        disabled={updateRenown.isPending}
                        onChange={(v) => void applyRenown({ whole_collection: v }, v ? 'Режим: вся коллекция' : 'Режим: ядро коллекции')}
                        fallbackLabel="Ядро коллекции"
                        fallbackHint="Дозаполняется только ядро коллекции — работы с несколькими изданиями, экранизацией или библиотечным рейтингом. Дешевле и объективнее: внешний запрос уходит туда, где известность вероятна."
                        warning="Вся коллекция: счётчики запрашиваются и для одиночных безвестных работ — сотни тысяч запросов, очень долго. Обычно достаточно ядра."
                      />
                      <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1.5">
                          <label htmlFor="renown-fl-rpm" className="text-sm">
                            Фантлаб, зап./мин
                          </label>
                          <Input id="renown-fl-rpm" type="number" min={0} value={flRpmN} onChange={(e) => setFlRpmN(e.target.value)} />
                        </div>
                        <div className="space-y-1.5">
                          <label htmlFor="renown-ol-rpm" className="text-sm">
                            OpenLibrary, зап./мин
                          </label>
                          <Input id="renown-ol-rpm" type="number" min={0} value={olRpmN} onChange={(e) => setOlRpmN(e.target.value)} />
                        </div>
                        <div className="space-y-1.5">
                          <label htmlFor="renown-wd-rpm" className="text-sm">
                            Wikidata, зап./мин
                          </label>
                          <Input id="renown-wd-rpm" type="number" min={0} value={wdRpmN} onChange={(e) => setWdRpmN(e.target.value)} />
                        </div>
                      </div>
                      <div className="flex flex-wrap gap-2">
                        {renownOnce ? (
                          <Button variant="outline" size="sm" onClick={onStopRenown} disabled={stopRenown.isPending}>
                            <Square className="size-4" aria-hidden />
                            {stopRenown.isPending ? 'Остановка…' : 'Остановить проход'}
                          </Button>
                        ) : (
                          <Button variant="outline" size="sm" onClick={onRunRenown} disabled={renownRunning || runRenown.isPending}>
                            <Flame className="size-4" aria-hidden />
                            {runRenown.isPending ? 'Запуск…' : 'Прогнать разово'}
                          </Button>
                        )}
                        <Button variant="outline" size="sm" onClick={onResetRenown} disabled={resetRenown.isPending}>
                          <RotateCcw className="size-4" aria-hidden />
                          {resetRenown.isPending ? 'Сброс…' : 'Сбросить неудачные'}
                        </Button>
                      </div>
                      {renownRunning ? <RunningDot continuous={!renownOnce} /> : null}
                    </div>
                  ) : null}
                </>
              ) : null}
              {nCov?.by_source && Object.keys(nCov.by_source).length > 0 ? (
                <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  <span className="w-full text-muted-foreground/80">Найдено источниками:</span>
                  {Object.entries(nCov.by_source)
                    .sort((a, b) => b[1] - a[1])
                    .map(([src, n]) => (
                      <span key={src} className="tabular-nums">
                        {SOURCE_LABELS[src] ?? src}: {n}
                      </span>
                    ))}
                </div>
              ) : null}
            </TypeRow>

            {/* ГРУППИРОВКА ИЗДАНИЙ (works) */}
            <TypeRow
              title="Группировка изданий"
              mode={wgMode}
              coverage={wgCov ? `${wgCov.multi_edition_works} мульти` : '—'}
            >
              <ModeSelector
                idPrefix="workgroup"
                value={wgMode}
                twoState
                help={MODE_HELP_WG}
                disabled={updateWg.isPending || wgRegrouping}
                onChange={(m) => void applyWg({ enabled: m === 'bg' }, `Режим: ${MODE_LABEL[m]}`)}
              />
              {wgRegrouping ? (
                <Callout icon={<Loader2 className="mt-0.5 size-3.5 shrink-0 animate-spin" aria-hidden />}>
                  <div className="w-full space-y-2">
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                      <span className="text-pretty">
                        Идёт массовый разбор работ
                        {wgRegroupTotal > 0 ? (
                          <>
                            {' '}
                            — обработано{' '}
                            <span className="font-medium tabular-nums">
                              {wgRegroupDone} из {wgRegroupTotal}
                            </span>
                          </>
                        ) : null}
                        . Воркер приостановлен автоматически и вернётся в прежнее состояние
                        после завершения.
                      </span>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={onStopRegroup}
                        disabled={stopRegroup.isPending}
                      >
                        <Square className="size-4" aria-hidden />
                        {stopRegroup.isPending ? 'Отмена…' : 'Отменить разбор'}
                      </Button>
                    </div>
                    {wgRegroupTotal > 0 ? (
                      <div
                        className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
                        role="progressbar"
                        aria-valuemin={0}
                        aria-valuemax={wgRegroupTotal}
                        aria-valuenow={wgRegroupDone}
                        aria-label="Прогресс разбора работ"
                      >
                        <div
                          className="h-full rounded-full bg-foreground/70 transition-[width] duration-500"
                          style={{ width: `${Math.min(100, Math.round((wgRegroupDone / wgRegroupTotal) * 100))}%` }}
                        />
                      </div>
                    ) : null}
                  </div>
                </Callout>
              ) : null}
              <Callout icon={<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
                Несколько fb2-файлов одной книги (разные издания/переводы) схлопываются в одну
                карточку. Tier-1 — локально (название+язык, оригинал из «src-title-info», точный
                дубль). Tier-2 — внешний Work ID (OpenLibrary / Wikidata). Никогда не сливает книги
                разных авторов; при сомнении оставляет отдельной книгой.
              </Callout>
              {/* Глобальный пересбор: доступен независимо от режима воркера —
                  джоба сама приостанавливает и восстанавливает его. Кнопка
                  прячется на время идущего разбора (его занимает индикатор). */}
              {!wgRegrouping ? (
                <div className="space-y-1.5">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={onRegroupAll}
                    disabled={regroupAll.isPending}
                  >
                    <RotateCcw className="size-4" aria-hidden />
                    {regroupAll.isPending ? 'Запуск…' : 'Пересобрать группировки'}
                  </Button>
                  <p className="text-xs text-muted-foreground text-pretty">
                    Разбирает все объединения и собирает заново по текущим правилам — чинит
                    ошибочно слитые книги (например, разные тома серии в одной карточке).
                    Ручные объединения тоже пересобираются.
                  </p>
                </div>
              ) : null}
              {wgMode === 'bg' ? (
                <>
                  <div className="space-y-2">
                    <FieldLabel>Внешние источники (Tier-2)</FieldLabel>
                    <SourceSwitch
                      id="wg-src-ol"
                      label="OpenLibrary Work (ISBN → работа)"
                      checked={wq.data?.openlibrary ?? false}
                      disabled={updateWg.isPending}
                      onChange={(v) => void applyWg({ openlibrary: v }, v ? 'OpenLibrary включён' : 'OpenLibrary выключен')}
                    />
                    <SourceSwitch
                      id="wg-src-wd"
                      label="Wikidata (P629)"
                      checked={wq.data?.wikidata ?? false}
                      disabled={updateWg.isPending}
                      onChange={(v) => void applyWg({ wikidata: v }, v ? 'Wikidata включён' : 'Wikidata выключен')}
                    />
                    <p className="text-xs text-muted-foreground text-pretty">
                      Оба выключены — работает только локальный Tier-1 (без сети). Межъязыковое
                      слияние переводов в основном опирается на Tier-2.
                    </p>
                  </div>
                  <div className="space-y-3 border-t border-border pt-3">
                    <ScopeControl
                      whole={wq.data?.whole_collection ?? false}
                      disabled={updateWg.isPending}
                      onChange={(v) => void applyWg({ whole_collection: v }, v ? 'Режим: вся коллекция' : 'Режим: после edition-скана')}
                      warning="Вся коллекция: группировать даже книги, у которых локальный edition-проход fb2 ещё не прошёл (src-ключей нет). Внешних запросов много, очень долго."
                    />
                    <div className="flex flex-wrap gap-2">
                      {wgOnce ? (
                        <Button variant="outline" size="sm" onClick={onStopWg} disabled={stopWg.isPending}>
                          <Square className="size-4" aria-hidden />
                          {stopWg.isPending ? 'Остановка…' : 'Остановить проход'}
                        </Button>
                      ) : (
                        <Button variant="outline" size="sm" onClick={onRunWg} disabled={wgRunning || wgRegrouping || runWg.isPending}>
                          <Flame className="size-4" aria-hidden />
                          {runWg.isPending ? 'Запуск…' : 'Прогнать разово'}
                        </Button>
                      )}
                    </div>
                    {wgRunning ? <RunningDot continuous={!wgOnce} /> : null}
                  </div>
                </>
              ) : null}
              {wgCov ? (
                <p className="text-xs tabular-nums text-muted-foreground text-pretty">
                  {wgCov.works} книг из {wgCov.books} изданий · с несколькими изданиями:{' '}
                  {wgCov.multi_edition_works} · обработано {wgCov.scanned}/{wgCov.books}
                </p>
              ) : null}
            </TypeRow>
          </section>

          {dirty ? <SaveBar saving={saving} onSave={onSave} onReset={onReset} canSave={!saveInvalid} /> : null}
        </>
      )}
    </article>
  );
}
