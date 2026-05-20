import { useEffect, useState } from 'react';
import { Download, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { installPromptStore } from '@/lib/pwa';

/**
 * InstallPromptBanner — компактный banner внизу экрана с предложением
 * установить PWA. Появляется когда:
 *   - браузер эмитнул beforeinstallprompt (т.е. поддержка установки + не
 *     установлено уже + есть manifest + SW зарегистрирован);
 *   - пользователь не отклонил banner в последние 30 дней.
 *
 * UX-решения:
 *   - Banner внизу, не сверху: меньше мешает чтению карточек книг;
 *     mobile-first привычка (FAB-зона).
 *   - Две кнопки: «Установить» (CTA) и крестик «не сейчас». Никакой
 *     явной кнопки «никогда не показывать» — 30 дней TTL на dismiss
 *     этого достаточно (а если пользователь установит — событие
 *     appinstalled очистит локалстораж).
 *   - Sticky-position с inset-bottom для notch'-aware устройств.
 *
 * Не показывается на iOS Safari: там beforeinstallprompt не эмитится,
 * install происходит через Share → Add to Home Screen вручную.
 * Можно добавить отдельный iOS-banner с инструкцией, но это полировка
 * для follow-up'а.
 */
export function InstallPromptBanner() {
  const [available, setAvailable] = useState(false);

  useEffect(() => {
    // Подписываемся на изменения; initial state читаем сразу — store мог
    // уже получить beforeinstallprompt до маунта компонента.
    setAvailable(installPromptStore.available());
    return installPromptStore.subscribe(setAvailable);
  }, []);

  if (!available) return null;

  return (
    <div
      className="fixed inset-x-0 bottom-0 z-40 pb-[env(safe-area-inset-bottom,0)] pointer-events-none"
      role="region"
      aria-label="Установить приложение"
    >
      <div className="mx-auto max-w-md p-3 pointer-events-auto">
        <div className="flex items-center gap-3 rounded-lg border border-border bg-card shadow-lg p-3">
          <Download className="size-5 text-muted-foreground shrink-0" aria-hidden />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium">Установить skriptes</p>
            <p className="text-xs text-muted-foreground">
              Откроется как обычное приложение, без браузера.
            </p>
          </div>
          <Button
            size="sm"
            onClick={() => {
              void installPromptStore.prompt();
            }}
          >
            Установить
          </Button>
          <Button
            size="icon"
            variant="ghost"
            aria-label="Скрыть"
            onClick={() => installPromptStore.dismiss()}
          >
            <X className="size-4" aria-hidden />
          </Button>
        </div>
      </div>
    </div>
  );
}
