import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Flame, Square, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { AdminTabs } from '@/components/AdminTabs';
import {
  useCoverCacheSettings,
  useUpdateCoverCacheSettings,
  useClearCoverCache,
  usePrewarmCoverCache,
  useStopPrewarmCoverCache,
} from '@/lib/admin';
import { ApiError } from '@/lib/api';

/**
 * AdminCoverCachePage — /admin/cover-cache. Рантайм-настройки кэша
 * обложек: бюджет (LRU-эвикция), порог свободного места, тумблер прогрева.
 * Плюс статистика (размер кэша, свободно на диске) и кнопка «Очистить».
 *
 * Дефолты живут на бэке (settings.DefaultCoverConfig); здесь правим
 * оверрайды. Лимиты и тумблер прогрева применяются в рантайме (без
 * рестарта); плюс кнопка разового прогона с возможностью остановки.
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

  const [maxMB, setMaxMB] = useState('');
  const [minFreeMB, setMinFreeMB] = useState('');
  const [prewarm, setPrewarm] = useState(false);

  // Заполняем форму, когда данные пришли.
  useEffect(() => {
    if (q.data) {
      setMaxMB(String(q.data.cache_max_mb));
      setMinFreeMB(String(q.data.cache_min_free_mb));
      setPrewarm(q.data.prewarm);
    }
  }, [q.data]);

  const maxN = Number(maxMB);
  const minFreeN = Number(minFreeMB);
  const invalid =
    maxMB === '' || minFreeMB === '' || Number.isNaN(maxN) || Number.isNaN(minFreeN) || maxN < 0 || minFreeN < 0;
  const lowFloorWarn = !Number.isNaN(minFreeN) && minFreeN < MIN_FREE_WARN_MB;

  const onSave = async () => {
    if (invalid) return;
    try {
      await update.mutateAsync({
        cache_max_mb: maxN,
        cache_min_free_mb: minFreeN,
        prewarm,
      });
      toast.success('Настройки кэша сохранены');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
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
    <article className="space-y-6">
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
              <CardTitle className="text-base">Состояние</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 pt-2 sm:max-w-md">
              <div className="grid grid-cols-2 gap-3 text-sm">
                <span className="text-muted-foreground">Размер кэша</span>
                <span className="tabular-nums">{formatBytes(q.data?.cache_size_bytes ?? 0)}</span>
                <span className="text-muted-foreground">Свободно на диске</span>
                <span className="tabular-nums">{formatBytes(q.data?.free_bytes ?? -1)}</span>
              </div>

              {/* Действия над кэшем — отдельно от сохранения настроек. */}
              <div className="flex flex-wrap gap-2">
                {mode === 'once' ? (
                  <Button variant="outline" onClick={onStopPrewarm} disabled={stopPrewarm.isPending}>
                    <Square className="size-4" aria-hidden />
                    {stopPrewarm.isPending ? 'Остановка…' : 'Остановить прогрев'}
                  </Button>
                ) : (
                  <Button
                    variant="outline"
                    onClick={onPrewarmNow}
                    disabled={prewarm || running || prewarmNow.isPending}
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

              {running ? (
                <p className="flex items-center gap-2 text-xs text-muted-foreground">
                  <span className="inline-block size-2 animate-pulse rounded-full bg-primary" aria-hidden />
                  {mode === 'continuous'
                    ? 'Непрерывный прогрев активен.'
                    : 'Идёт разовый прогон прогрева…'}
                </p>
              ) : prewarm ? (
                <p className="text-xs text-muted-foreground">
                  «Прогреть сейчас» недоступно при включённом фоновом прогреве — он обрабатывает
                  всю коллекцию.
                </p>
              ) : null}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Настройки</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4 pt-2 sm:max-w-md">
              <div className="space-y-1.5">
                <Label htmlFor="cache-max">Бюджет кэша, МБ</Label>
                <Input
                  id="cache-max"
                  type="number"
                  min={0}
                  value={maxMB}
                  onChange={(e) => setMaxMB(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  При превышении вытесняются давно не запрашивавшиеся (LRU). 0 — без лимита
                  (хранить всё; только для прогрева на диске с запасом).
                </p>
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
                  <p className="text-xs text-amber-500">
                    Безопаснее держать ≥ {MIN_FREE_WARN_MB} МБ: слишком низкий порог повышает риск
                    забить диск.
                  </p>
                ) : null}
              </div>

              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  className="size-4 rounded border-input"
                  checked={prewarm}
                  onChange={(e) => setPrewarm(e.target.checked)}
                />
                <span>Фоновый прогрев обложек всей коллекции</span>
              </label>
              <p className="-mt-2 text-xs text-muted-foreground">
                Извлекает обложки для всей библиотеки заранее (нужен запас места). При включении
                бюджет фактически не ограничивает рост — ориентируйтесь на порог свободного
                места. Включение/выключение применяется сразу при сохранении.
              </p>

              {/* Одна кнопка внизу секции — сохраняет ВСЕ настройки выше. */}
              <Button className="w-full sm:w-auto" onClick={onSave} disabled={invalid || update.isPending}>
                {update.isPending ? 'Сохранение…' : 'Сохранить'}
              </Button>
            </CardContent>
          </Card>
        </>
      )}
    </article>
  );
}
