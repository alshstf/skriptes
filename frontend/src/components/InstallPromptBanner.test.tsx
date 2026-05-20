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
    // Re-init store: в JSDOM event listener'ы сохраняются между тестами,
    // но deferred-event у store свой; обнуляем через приватный hack —
    // фактически достаточно отправить новый beforeinstallprompt.
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
});
