/**
 * pwa.ts — клиентская обвязка над service worker'ом и install prompt'ом.
 *
 * Содержит:
 *  - registerPWA(): регистрирует SW через virtual:pwa-register; toast'ит
 *    «обновление доступно» / «готов к offline».
 *  - InstallPromptStore: подписка на beforeinstallprompt event; хранит
 *    deferred-объект для последующего .prompt() из UI-кнопки. localStorage
 *    запоминает dismiss на 30 дней, чтобы не маячить.
 *
 * Не зависит от React — модули вызываются из main.tsx и из компонента
 * banner'а через хук-обёртку.
 */

import { toast } from 'sonner';

// ── Service worker ──────────────────────────────────────────────────

/**
 * registerPWA — оборачивает virtual:pwa-register (генерится vite-plugin-pwa
 * во время билда). В тестах модуль не существует, поэтому динамический
 * import; на dev-сервере без enabled.devOptions он тоже отсутствует —
 * молча выходим.
 *
 * autoUpdate (см. vite.config.ts) сам активирует новый SW при следующей
 * навигации; toast «обновление доступно» — просто визуальный сигнал
 * пользователю, что вот сейчас он увидит свежую версию.
 *
 * Пропускаем регистрацию на *.localhost хостах. Caddy там выдаёт TLS-
 * сертификат через локальный CA, которому system keychain браузера не
 * доверяет по умолчанию. Браузер принимает cert на уровне навигации
 * (пользователь жмёт «Принять»), но FETCH ИЗ SW идёт отдельным контекстом,
 * без UI для подтверждения, и падает с network error. Workbox в этом
 * случае на стратегии NetworkOnly (для /api/auth/*) возвращает
 * «no-response: no-response», и страница ломается на первом же
 * /api/auth/me. PWA-фича на dev-стенде всё равно не нужна — здесь
 * проще зайти как обычное web-приложение без offline-shell.
 *
 * В production (реальный домен + Let's Encrypt) cert валиден,
 * keychain ему доверяет, SW работает как ожидается.
 */
export async function registerPWA(): Promise<void> {
  if (typeof window === 'undefined' || !('serviceWorker' in navigator)) {
    return;
  }
  if (isLocalhostHost(window.location.hostname)) {
    // Тихо — это ожидаемое поведение на dev-стенде, не повод для warn'а.
    return;
  }
  try {
    const mod = (await import(/* @vite-ignore */ 'virtual:pwa-register')) as {
      registerSW: (opts: {
        immediate?: boolean;
        onNeedRefresh?: () => void;
        onOfflineReady?: () => void;
        onRegisterError?: (err: unknown) => void;
      }) => () => Promise<void>;
    };
    mod.registerSW({
      immediate: true,
      onOfflineReady: () => {
        toast.success('Готово к работе оффлайн', {
          description: 'Открытые карточки и обложки кэшированы.',
        });
      },
      onNeedRefresh: () => {
        toast.info('Доступна новая версия', {
          description: 'Обновится при следующей навигации.',
          duration: 6000,
        });
      },
      onRegisterError: (err) => {
        // Не падаем — SW не критичен. В dev режиме virtual:pwa-register
        // в no-op варианте может не запуститься; молча логируем.
        console.warn('PWA: SW registration error', err);
      },
    });
  } catch {
    // virtual:pwa-register отсутствует (dev без enabled.devOptions, тесты).
    // Это ожидаемо; ничего не делаем.
  }
}

// ── Install prompt ──────────────────────────────────────────────────

/**
 * BeforeInstallPromptEvent — нестандартное Chrome-only событие. TS не
 * знает о нём, описываем сами.
 *
 * Когда браузер решает что приложение установимое (есть manifest +
 * service worker + сайт под HTTPS), он эмитит beforeinstallprompt и
 * откладывает свой нативный UI «Установить?» до .prompt() со стороны
 * сайта. Мы перехватываем event, prevent'им default, держим у себя и
 * показываем кастомный banner; кнопка «Установить» в banner'е дёргает
 * deferred.prompt().
 */
interface BeforeInstallPromptEvent extends Event {
  readonly platforms: string[];
  readonly userChoice: Promise<{ outcome: 'accepted' | 'dismissed'; platform: string }>;
  prompt(): Promise<void>;
}

type Listener = (available: boolean) => void;

const DISMISS_KEY = 'pwa-install-dismissed-at';
const DISMISS_TTL_MS = 30 * 24 * 60 * 60 * 1000; // 30 дней

/**
 * installPromptStore — singleton-state. Регистрируется один раз в main.tsx
 * через initInstallPromptStore(); компонент banner'а подписывается через
 * subscribe + читает available().
 */
class InstallPromptStore {
  private deferred: BeforeInstallPromptEvent | null = null;
  private listeners: Set<Listener> = new Set();
  private installed = false;

  init(): void {
    if (typeof window === 'undefined') return;
    window.addEventListener('beforeinstallprompt', (e) => {
      e.preventDefault();
      this.deferred = e as BeforeInstallPromptEvent;
      this.notify();
    });
    window.addEventListener('appinstalled', () => {
      this.installed = true;
      this.deferred = null;
      // На случай если пользователь установил вне нашего banner'а
      // (через меню браузера) — очистим dismiss-метку.
      try {
        localStorage.removeItem(DISMISS_KEY);
      } catch {
        // в тестах / приватном режиме может бросить — игнорим
      }
      this.notify();
    });
  }

  /** available — можно ли сейчас показать install-banner. */
  available(): boolean {
    if (this.installed) return false;
    if (!this.deferred) return false;
    if (this.recentlyDismissed()) return false;
    return true;
  }

  /**
   * prompt — вызвать нативный диалог браузера. Возвращает 'accepted' /
   * 'dismissed'. После любого исхода deferred-event одноразовый, его
   * нельзя переиспользовать; чистим.
   */
  async prompt(): Promise<'accepted' | 'dismissed' | 'unavailable'> {
    if (!this.deferred) return 'unavailable';
    const ev = this.deferred;
    this.deferred = null;
    this.notify();
    try {
      await ev.prompt();
      const { outcome } = await ev.userChoice;
      if (outcome === 'dismissed') {
        this.markDismissed();
      }
      return outcome;
    } catch {
      return 'dismissed';
    }
  }

  /** dismiss — пользователь нажал «не сейчас» в нашем banner'е. */
  dismiss(): void {
    this.markDismissed();
    this.notify();
  }

  subscribe(fn: Listener): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /**
   * __resetForTest — обнуляет внутреннее состояние (deferred + installed).
   * Только для unit-тестов: singleton-store сохраняет deferred между
   * вызовами, и iOS-тест может видеть «chrome variant available» из-за
   * утечки от предыдущего test'а. В prod не используется.
   */
  __resetForTest(): void {
    this.deferred = null;
    this.installed = false;
    this.notify();
  }

  private notify(): void {
    const v = this.available();
    this.listeners.forEach((fn) => fn(v));
  }

  private markDismissed(): void {
    markDismissed();
  }

  private recentlyDismissed(): boolean {
    return recentlyDismissed();
  }
}

export const installPromptStore = new InstallPromptStore();

// ── iOS install hint ────────────────────────────────────────────────
//
// На iOS Safari beforeinstallprompt НЕ эмитится: установка происходит
// исключительно через Share → «На экран „Домой"», вручную. Соответственно
// нам нечего .prompt() — мы можем только показать пользователю инструкцию.
//
// Условия показа iOS-баннера:
//   - User-agent похож на Mobile Safari на iOS / iPadOS;
//   - Не уже в standalone-режиме (`navigator.standalone === true` —
//     iOS-специфичный API; true = приложение запущено с home screen);
//   - Не отклонено в последние 30 дней (тот же DISMISS_KEY что у
//     Chrome-баннера — если человек отказался на одном устройстве и
//     зашёл на другом, разумно тоже не маячить).
//
// Намеренно не пытаемся ловить in-app браузеры (Instagram/WeChat и т.п.):
// тривиально на UA не выделить, а ложный позитив (показ инструкции там,
// где её исполнить нельзя) лучше чем ложный негатив (молчание там, где
// человек реально может установить через настоящий Safari).

interface NavigatorStandalone extends Navigator {
  /** iOS Safari-специфичный нестандартный property. */
  standalone?: boolean;
}

/**
 * isIOSSafari — UA-based detect Mobile Safari на iOS/iPadOS.
 *
 * UA-sniffing считается анти-паттерном, но для iOS-detection это
 * единственный путь: нет feature-detection для «эта Safari умеет
 * Add to Home Screen». Список проверок:
 *  - наличие "iPad" / "iPhone" / "iPod" в UA (iOS Safari, Chrome iOS,
 *    Edge iOS — всё на WebKit, но beforeinstallprompt никто из них
 *    не эмитит, инструкция актуальна для всех);
 *  - iPadOS 13+ маскируется под macOS — дополнительно ловим по
 *    maxTouchPoints > 1 на macOS-UA как сигнал «iPad».
 */
export function isIOSSafari(): boolean {
  if (typeof navigator === 'undefined') return false;
  const ua = navigator.userAgent;
  if (/iPhone|iPad|iPod/.test(ua)) return true;
  // iPadOS 13+ выдаёт себя за Mac. Touch на macOS — почти всегда iPad.
  if (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1) return true;
  return false;
}

/**
 * isStandaloneIOS — приложение уже установлено и запущено с home screen
 * на iOS. Используем nullish-check вместо `=== true` чтобы не падать на
 * браузерах где свойства нет вообще.
 */
export function isStandaloneIOS(): boolean {
  if (typeof navigator === 'undefined') return false;
  const nav = navigator as NavigatorStandalone;
  return nav.standalone === true;
}

/**
 * isIOSInstallable — пора ли показать iOS-баннер с инструкцией?
 *
 * Дополнительная проверка `installPromptStore.available()` нужна на
 * случай чудес типа «Chrome iOS внезапно эмитнул beforeinstallprompt»
 * (не должно случиться, но если случится — пусть Chrome-баннер
 * выигрывает, у него реальная нативная кнопка).
 */
export function isIOSInstallable(): boolean {
  if (!isIOSSafari()) return false;
  if (isStandaloneIOS()) return false;
  if (installPromptStore.available()) return false;
  if (recentlyDismissed()) return false;
  return true;
}

/**
 * dismissIOSInstall — закрыть iOS-баннер; та же localStorage-метка,
 * что и у Chrome-баннера, чтобы 30-day TTL применялся универсально.
 */
export function dismissIOSInstall(): void {
  markDismissed();
}

function recentlyDismissed(): boolean {
  try {
    const raw = localStorage.getItem(DISMISS_KEY);
    if (!raw) return false;
    const ts = Number(raw);
    if (Number.isNaN(ts)) return false;
    return Date.now() - ts < DISMISS_TTL_MS;
  } catch {
    return false;
  }
}

function markDismissed(): void {
  try {
    localStorage.setItem(DISMISS_KEY, String(Date.now()));
  } catch {
    // ignore
  }
}

/**
 * isLocalhostHost — true для хостов где TLS обслуживается локальным CA
 * (нет system-trust → SW fetches падают). Покрывает:
 *  - "localhost", "127.0.0.1", "::1" — bare loopback;
 *  - "*.localhost" — Caddy auto-issued cert через локальный CA
 *    (в нашем dev-стенде это `skriptes.localhost`);
 *  - "*.local" / "*.test" — рекомендованные RFC-зарезервированные
 *    суффиксы для разработки.
 *
 * НЕ покрывает: реальные домены (production), приватные IP подсетей —
 * для подсетей home network может быть Let's Encrypt через DNS-01, мы
 * НЕ хотим выключать SW там.
 */
export function isLocalhostHost(hostname: string): boolean {
  if (hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '::1') {
    return true;
  }
  return /\.(localhost|local|test)$/i.test(hostname);
}
