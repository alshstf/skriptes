import { useEffect, useState } from 'react';
import { Download, Share, Plus, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { dismissIOSInstall, installPromptStore, isIOSInstallable } from '@/lib/pwa';

/**
 * InstallPromptBanner — компактный banner внизу экрана с предложением
 * установить PWA. Один компонент с двумя визуальными вариантами:
 *
 *  1. Chrome / Edge / Firefox-PWA (event-driven) — appears when
 *     beforeinstallprompt event fired AND not dismissed. CTA-кнопка
 *     «Установить» дёргает нативный browser dialog.
 *
 *  2. iOS Safari (instruction-only) — appears when isIOSInstallable():
 *     UA = mobile Safari, NOT уже standalone, NOT dismissed. На iOS
 *     beforeinstallprompt не эмитится, .prompt() недоступен —
 *     показываем картинку с инструкцией «Share → На экран „Домой"».
 *
 * UX-решения общие:
 *   - Banner внизу, не сверху: меньше мешает чтению карточек книг;
 *     mobile-first привычка (FAB-зона).
 *   - Sticky-position с inset-bottom для notch'-aware устройств.
 *   - dismiss-метка общая (localStorage `pwa-install-dismissed-at`,
 *     30 дней TTL) — отказался на одном пути, не маячим и на другом.
 *
 * Приоритет: если по какой-то причине доступны оба пути (Chrome iOS
 * вдруг начал эмитить beforeinstallprompt — пока такого нет, но
 * страховка) — Chrome-вариант выигрывает: у него настоящая кнопка
 * вместо инструкции.
 */
export function InstallPromptBanner() {
  const [variant, setVariant] = useState<'chrome' | 'ios' | null>(null);

  useEffect(() => {
    // Определение варианта при mount: Chrome (event-driven) vs iOS
    // (instruction-only) vs ничего. Изменения Chrome-пути слушаем через
    // store.subscribe; iOS-путь статичен (UA не меняется), один initial
    // check достаточен.
    function recalc() {
      if (installPromptStore.available()) {
        setVariant('chrome');
      } else if (isIOSInstallable()) {
        setVariant('ios');
      } else {
        setVariant(null);
      }
    }
    recalc();
    return installPromptStore.subscribe(recalc);
  }, []);

  if (variant === null) return null;

  return (
    <div
      className="fixed inset-x-0 bottom-0 z-40 pb-[env(safe-area-inset-bottom,0)] pointer-events-none"
      role="region"
      aria-label="Установить приложение"
    >
      <div className="mx-auto max-w-md p-3 pointer-events-auto">
        {variant === 'chrome' ? <ChromeBanner /> : <IOSBanner onDismiss={() => setVariant(null)} />}
      </div>
    </div>
  );
}

/** ChromeBanner — нативный prompt доступен, одной кнопкой устанавливаем. */
function ChromeBanner() {
  return (
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
  );
}

/**
 * IOSBanner — инструкция: «Поделиться» → «На экран „Домой"». Без
 * CTA-кнопки (нечего вызывать — пользователь действует руками).
 *
 * Иконки lucide ≠ нативные iOS-иконки share/plus 1-в-1, но визуально
 * узнаются: квадрат-со-стрелкой и плюс-в-квадрате. На iPad share-кнопка
 * обычно сверху-справа, на iPhone — снизу; формулировка нейтральная
 * «в меню Поделиться» подходит и тому, и другому.
 *
 * onDismiss колбэк нужен потому что у iOS-пути нет внешнего store с
 * subscribe — родительский компонент пересчитает variant сам после
 * close-click'а через установленную в pwa.ts метку (через recalc на
 * следующем mount).
 */
function IOSBanner({ onDismiss }: { onDismiss: () => void }) {
  return (
    <div className="flex items-start gap-3 rounded-lg border border-border bg-card shadow-lg p-3">
      <Download className="size-5 text-muted-foreground shrink-0 mt-0.5" aria-hidden />
      <div className="flex-1 min-w-0 space-y-1">
        <p className="text-sm font-medium">Добавить skriptes на экран «Домой»</p>
        <p className="text-xs text-muted-foreground flex items-center flex-wrap gap-1">
          В меню
          <Share className="inline size-3.5 -mt-0.5" aria-label="Поделиться" />
          выберите
          <span className="inline-flex items-center gap-0.5 rounded border border-border px-1 py-0.5 text-[10px]">
            <Plus className="size-3" aria-hidden /> На экран «Домой»
          </span>
        </p>
      </div>
      <Button
        size="icon"
        variant="ghost"
        aria-label="Скрыть"
        onClick={() => {
          dismissIOSInstall();
          onDismiss();
        }}
      >
        <X className="size-4" aria-hidden />
      </Button>
    </div>
  );
}
