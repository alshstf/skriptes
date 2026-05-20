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
 */
export async function registerPWA(): Promise<void> {
  if (typeof window === 'undefined' || !('serviceWorker' in navigator)) {
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

  private notify(): void {
    const v = this.available();
    this.listeners.forEach((fn) => fn(v));
  }

  private markDismissed(): void {
    try {
      localStorage.setItem(DISMISS_KEY, String(Date.now()));
    } catch {
      // ignore
    }
  }

  private recentlyDismissed(): boolean {
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
}

export const installPromptStore = new InstallPromptStore();
