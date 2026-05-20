import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { InstallPromptBanner } from './InstallPromptBanner';
import { installPromptStore } from '@/lib/pwa';

/**
 * InstallPromptBanner — UI-обёртка над installPromptStore. Тесты
 * фиксируют контракт «banner появляется только когда store говорит
 * available; dismiss и prompt вызывают правильные методы».
 *
 * Сам store тестируется отдельно (lifecycle через beforeinstallprompt
 * event покрывается отдельным spec'ом если потребуется); здесь — только
 * рендер.
 */

function emitBeforeInstallPrompt(promptMock: () => Promise<void> = () => Promise.resolve()) {
  // Конструируем минимально-валидный BeforeInstallPromptEvent. JSDOM
  // не реализует его, поэтому используем CustomEvent + расширяем поля.
  const ev = new Event('beforeinstallprompt') as Event & {
    prompt: () => Promise<void>;
    userChoice: Promise<{ outcome: 'accepted' | 'dismissed'; platform: string }>;
    platforms: string[];
  };
  ev.prompt = promptMock;
  ev.userChoice = Promise.resolve({ outcome: 'accepted', platform: 'web' });
  ev.platforms = ['web'];
  window.dispatchEvent(ev);
}

describe('InstallPromptBanner', () => {
  beforeEach(() => {
    // Чистый storage для каждого теста, чтобы dismiss-метка не утекала.
    window.localStorage.clear();
    // Singleton-store держит deferred между тестами — без сброса
    // следующий тест видит «Chrome banner available» от прошлого
    // прогона и не показывает iOS-вариант.
    installPromptStore.__resetForTest();
    installPromptStore.init();
  });
  afterEach(() => {
    window.localStorage.clear();
  });

  it('не рендерится пока не было beforeinstallprompt', () => {
    render(<InstallPromptBanner />);
    expect(screen.queryByText('Установить skriptes')).not.toBeInTheDocument();
  });

  it('появляется после beforeinstallprompt event', async () => {
    render(<InstallPromptBanner />);
    await act(async () => {
      emitBeforeInstallPrompt();
    });
    expect(await screen.findByText('Установить skriptes')).toBeInTheDocument();
  });

  it('клик "Установить" вызывает prompt() и принимает', async () => {
    const promptMock = vi.fn(() => Promise.resolve());
    render(<InstallPromptBanner />);
    await act(async () => {
      emitBeforeInstallPrompt(promptMock);
    });

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Установить' }));
    expect(promptMock).toHaveBeenCalledOnce();
  });

  it('клик "Скрыть" прячет banner и пишет dismiss-метку в localStorage', async () => {
    render(<InstallPromptBanner />);
    await act(async () => {
      emitBeforeInstallPrompt();
    });
    expect(screen.getByText('Установить skriptes')).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Скрыть' }));

    expect(screen.queryByText('Установить skriptes')).not.toBeInTheDocument();
    expect(window.localStorage.getItem('pwa-install-dismissed-at')).not.toBeNull();
  });

  it('не появляется после dismiss даже если новый beforeinstallprompt прилетел', async () => {
    // Симулируем: пользователь уже dismiss'нул раньше (метка свежая).
    window.localStorage.setItem('pwa-install-dismissed-at', String(Date.now()));
    render(<InstallPromptBanner />);
    await act(async () => {
      emitBeforeInstallPrompt();
    });
    expect(screen.queryByText('Установить skriptes')).not.toBeInTheDocument();
  });

  // ── iOS variant ───────────────────────────────────────────────────

  /**
   * stubIOSSafari — переключает navigator.userAgent на iOS Safari + сбрасывает
   * maxTouchPoints. Возвращает функцию-restore.
   *
   * Object.defineProperty потому что navigator.userAgent в jsdom — getter,
   * прямое присвоение даёт TypeError. iOS Safari UA включает "iPhone OS",
   * этого достаточно для регулярки в isIOSSafari().
   */
  function stubIOSSafari(): () => void {
    const realUA = Object.getOwnPropertyDescriptor(window.navigator, 'userAgent');
    Object.defineProperty(window.navigator, 'userAgent', {
      value:
        'Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1',
      configurable: true,
    });
    return () => {
      if (realUA) Object.defineProperty(window.navigator, 'userAgent', realUA);
    };
  }

  /** stubStandalone — делает вид что приложение уже установлено и запущено standalone. */
  function stubStandalone(value: boolean): () => void {
    const real = Object.getOwnPropertyDescriptor(window.navigator, 'standalone');
    Object.defineProperty(window.navigator, 'standalone', { value, configurable: true });
    return () => {
      if (real) {
        Object.defineProperty(window.navigator, 'standalone', real);
      } else {
        // @ts-expect-error iOS-only nonstandard property
        delete window.navigator.standalone;
      }
    };
  }

  it('iOS Safari + не standalone → показывает инструкцию «На экран Домой»', () => {
    const restore = stubIOSSafari();
    try {
      render(<InstallPromptBanner />);
      // Хедер iOS-баннера отличается от Chrome-баннера, проверяем именно его.
      expect(screen.getByText('Добавить skriptes на экран «Домой»')).toBeInTheDocument();
      // Инструкция содержит лейбл shareОНа экран Домой.
      expect(screen.getByText(/На экран «Домой»/)).toBeInTheDocument();
      // НЕ должно быть Chrome-CTA-кнопки «Установить».
      expect(screen.queryByRole('button', { name: 'Установить' })).not.toBeInTheDocument();
    } finally {
      restore();
    }
  });

  it('iOS Safari + standalone (уже установлено) → ничего не показываем', () => {
    const restoreUA = stubIOSSafari();
    const restoreSA = stubStandalone(true);
    try {
      render(<InstallPromptBanner />);
      expect(screen.queryByText(/На экран «Домой»/)).not.toBeInTheDocument();
      expect(screen.queryByText('Установить skriptes')).not.toBeInTheDocument();
    } finally {
      restoreSA();
      restoreUA();
    }
  });

  it('iOS dismiss → banner исчезает + dismiss-метка одна и та же что у Chrome', async () => {
    const restore = stubIOSSafari();
    try {
      render(<InstallPromptBanner />);
      expect(screen.getByText('Добавить skriptes на экран «Домой»')).toBeInTheDocument();

      const user = userEvent.setup();
      await user.click(screen.getByRole('button', { name: 'Скрыть' }));

      expect(screen.queryByText('Добавить skriptes на экран «Домой»')).not.toBeInTheDocument();
      // Ключ должен быть тем же, что у Chrome-варианта — общий 30-day TTL.
      expect(window.localStorage.getItem('pwa-install-dismissed-at')).not.toBeNull();
    } finally {
      restore();
    }
  });

  it('iOS dismiss → если пользователь ушёл на десктоп, Chrome-banner тоже не появится', async () => {
    // 1) Dismiss с iPhone.
    const restoreUA = stubIOSSafari();
    try {
      render(<InstallPromptBanner />);
      const user = userEvent.setup();
      await user.click(screen.getByRole('button', { name: 'Скрыть' }));
    } finally {
      restoreUA();
    }

    // 2) Тот же localStorage, обычный десктоп UA (default jsdom). Chrome
    // эмитит beforeinstallprompt — но recentlyDismissed() сработает, и
    // банера не должно быть.
    render(<InstallPromptBanner />);
    await act(async () => {
      emitBeforeInstallPrompt();
    });
    expect(screen.queryByText('Установить skriptes')).not.toBeInTheDocument();
  });
});
