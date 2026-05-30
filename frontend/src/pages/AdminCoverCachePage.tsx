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
} from '@/lib/admin';
import { ApiError } from '@/lib/api';

/**
 * AdminCoverCachePage — /admin/cover-cache. Две секции:
 *   - «Прогрев и состояние» — тумблер фонового прогрева (применяется сразу)
 *     рядом со своим индикатором, разовый прогон, очистка кэша, статистика;
 *   - «Лимиты кэша» — бюджет (LRU-эвикция) + порог свободного места, с
 *     явной кнопкой «Сохранить».
 *
 * Дефолты живут на бэке (settings.DefaultCoverConfig); здесь правим оверрайды.
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

export function AdminCoverCachePage() {
  const q = useCoverCacheSettings();
  const update = useUpdateCoverCacheSettings();
  const clear = useClearCoverCache();
  const prewarmNow = usePrewarmCoverCache();
  const stopPrewarm = useStopPrewarmCoverCache();

  const running = q.data?.prewarm_running ?? false;
  const mode = q.data?.prewarm_mode ?? 'off';
  // Прогрев — серверное состояние (live-тумблер), не поле формы.
  const prewarmOn = q.data?.prewarm ?? false;

  const [maxMB, setMaxMB] = useState('');
  const [minFreeMB, setMinFreeMB] = useState('');

  // Поля лимитов заполняем из данных ОДИН раз: поллинг прогрева рефетчит
  // каждые 2с, и пере-синхронизация на каждый рефетч затирала бы правки.
  const initialized = useRef(false);
  useEffect(() => {
    if (q.data && !initialized.current) {
      setMaxMB(String(q.data.cache_max_mb));
      setMinFreeMB(String(q.data.cache_min_free_mb));
      initialized.current = true;
    }
  }, [q.data]);

  const maxN = Number(maxMB);
  const minFreeN = Number(minFreeMB);
  const invalid =
    maxMB === '' || minFreeMB === '' || Number.isNaN(maxN) || Number.isNaN(minFreeN) || maxN < 0 || minFreeN < 0;
  const lowFloorWarn = !Number.isNaN(minFreeN) && minFreeN < MIN_FREE_WARN_MB;

  // Контекстозависимое сохранение лимитов: бар появляется при изменениях
  // (как на вкладке «Контент»). prewarm здесь не участвует — он live-тумблер.
  const dirty =
    !!q.data &&
    (maxMB !== String(q.data.cache_max_mb) || minFreeMB !== String(q.data.cache_min_free_mb));

  const onReset = () => {
    if (q.data) {
      setMaxMB(String(q.data.cache_max_mb));
      setMinFreeMB(String(q.data.cache_min_free_mb));
    }
  };

  // Лимиты: сохраняем бюджет + порог, прогрев оставляем как есть. После
  // сохранения канонизируем поля из ответа → dirty снимается надёжно.
  const onSave = async () => {
    if (invalid || !q.data) return;
    try {
      const saved = await update.mutateAsync({
        cache_max_mb: maxN,
        cache_min_free_mb: minFreeN,
        prewarm: prewarmOn,
      });
      setMaxMB(String(saved.cache_max_mb));
      setMinFreeMB(String(saved.cache_min_free_mb));
      toast.success('Лимиты кэша сохранены');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  // Прогрев: применяется сразу при переключении (с текущими сохранёнными
  // лимитами, а не с возможными несохранёнными правками в полях ниже).
  const onTogglePrewarm = async (next: boolean) => {
    if (!q.data) return;
    try {
      await update.mutateAsync({
        cache_max_mb: q.data.cache_max_mb,
        cache_min_free_mb: q.data.cache_min_free_mb,
        prewarm: next,
      });
      toast.success(next ? 'Фоновый прогрев включён' : 'Фоновый прогрев выключен');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось применить');
    }
  };

  const onClear = async () => {
    if (!window.confirm('Очистить весь кэш обложек? Они переизвлекутся из fb2 по мере просмотра.')) {
      return;
    }
    try {
      const r = await clear.mutateAsync();
      toast.success(`Кэш очищен: удалено файлов — ${r.removed}`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось очистить');
    }
  };

  const onPrewarmNow = async () => {
    try {
      await prewarmNow.mutateAsync();
      toast.success('Прогрев запущен в фоне');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось запустить прогрев');
    }
  };

  const onStopPrewarm = async () => {
    try {
      await stopPrewarm.mutateAsync();
      toast.success('Останавливаю прогрев…');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось остановить');
    }
  };

  return (
    // text-pretty (наследуется) — браузер не оставляет одно слово на
    // последней строке абзацев (сирот) в подсказках/описаниях ниже.
    <article className="space-y-6 text-pretty">
      <AdminTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Кэш обложек</h1>
        <p className="text-sm text-muted-foreground">
          Обложки извлекаются из fb2 по запросу и кэшируются. Бюджет ограничивает размер
          кэша (старые вытесняются), порог свободного места защищает раздел от переполнения.
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
              <CardTitle className="text-base">Прогрев и состояние</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 pt-2 sm:max-w-md">
              {/* Фоновый прогрев — switch (применяется сразу) + индикатор рядом. */}
              <div className="space-y-2">
                <div className="flex items-center gap-2.5">
                  <Switch
                    id="prewarm-toggle"
                    checked={prewarmOn}
                    disabled={update.isPending}
                    onCheckedChange={(v) => void onTogglePrewarm(v)}
                  />
                  <Label htmlFor="prewarm-toggle" className="cursor-pointer text-sm">
                    Фоновый прогрев обложек всей коллекции
                  </Label>
                </div>
                <p className="text-xs text-muted-foreground">
                  Извлекает обложки для всей библиотеки заранее (нужен запас места). При включении
                  бюджет фактически не ограничивает рост — ориентируйтесь на порог свободного
                  места. Применяется сразу.
                </p>
                {running ? (
                  <p className="flex items-center gap-2 text-xs text-muted-foreground">
                    <span className="inline-block size-2 animate-pulse rounded-full bg-primary" aria-hidden />
                    {mode === 'continuous'
                      ? 'Непрерывный прогрев активен.'
                      : 'Идёт разовый прогон прогрева…'}
                  </p>
                ) : null}
              </div>

              {/* Действия */}
              <div className="flex flex-wrap gap-2">
                {mode === 'once' ? (
                  <Button variant="outline" onClick={onStopPrewarm} disabled={stopPrewarm.isPending}>
                    <Square className="size-4" aria-hidden />
                    {stopPrewarm.isPending ? 'Остановка…' : 'Остановить прогон'}
                  </Button>
                ) : (
                  <Button
                    variant="outline"
                    onClick={onPrewarmNow}
                    disabled={prewarmOn || running || prewarmNow.isPending}
                  >
                    <Flame className="size-4" aria-hidden />
                    {prewarmNow.isPending ? 'Запуск…' : 'Прогреть сейчас'}
                  </Button>
                )}
                <Button variant="outline" onClick={onClear} disabled={clear.isPending}>
                  <Trash2 className="size-4" aria-hidden />
                  {clear.isPending ? 'Очистка…' : 'Очистить кэш'}
                </Button>
              </div>
              {prewarmOn && !running ? (
                <p className="-mt-2 text-xs text-muted-foreground">
                  «Прогреть сейчас» недоступно при включённом фоновом прогреве — он обрабатывает
                  всю коллекцию.
                </p>
              ) : null}

              {/* Состояние */}
              <div className="grid grid-cols-2 gap-3 border-t border-border pt-3 text-sm">
                <span className="text-muted-foreground">Размер кэша</span>
                <span className="tabular-nums">{formatBytes(q.data?.cache_size_bytes ?? 0)}</span>
                <span className="text-muted-foreground">Свободно на диске</span>
                <span className="tabular-nums">{formatBytes(q.data?.free_bytes ?? -1)}</span>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Лимиты кэша</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 pt-2 sm:max-w-md">
              <div className="space-y-1.5">
                <Label htmlFor="cache-max">Бюджет кэша, МБ</Label>
                <Input
                  id="cache-max"
                  type="number"
                  min={0}
                  value={maxMB}
                  disabled={prewarmOn}
                  onChange={(e) => setMaxMB(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  При превышении вытесняются давно не запрашивавшиеся (LRU). 0 — без лимита
                  (хранить всё; только для прогрева на диске с запасом).
                </p>
                {prewarmOn ? (
                  <Callout icon={<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
                    Бюджет не применяется, пока включён фоновый прогрев — рост кэша ограничивает
                    только порог свободного места.
                  </Callout>
                ) : null}
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
                <p className="text-xs text-muted-foreground">
                  Ниже него новые обложки не пишутся — защита раздела с базой от переполнения.
                </p>
                {lowFloorWarn ? (
                  <Callout icon={<AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
                    Безопаснее держать ≥ {MIN_FREE_WARN_MB} МБ: слишком низкий порог повышает риск
                    забить диск.
                  </Callout>
                ) : null}
              </div>

            </CardContent>
          </Card>

          {/* Контекстозависимое сохранение лимитов (как на вкладке «Контент»):
              бар появляется только при изменениях. */}
          {dirty ? (
            <SaveBar
              saving={update.isPending}
              onSave={onSave}
              onReset={onReset}
              canSave={!invalid}
            />
          ) : null}
        </>
      )}
    </article>
  );
}
