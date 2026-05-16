import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ExpandableText } from './ExpandableText';

describe('ExpandableText', () => {
  // jsdom не считает реальные размеры → scrollHeight === clientHeight === 0.
  // Чтобы протестировать ветку с "Развернуть", форсируем scrollHeight через
  // подмену прототипа Element.
  let origScroll: PropertyDescriptor | undefined;
  let origClient: PropertyDescriptor | undefined;

  beforeEach(() => {
    origScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight');
    origClient = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight');
  });
  afterEach(() => {
    if (origScroll) Object.defineProperty(HTMLElement.prototype, 'scrollHeight', origScroll);
    if (origClient) Object.defineProperty(HTMLElement.prototype, 'clientHeight', origClient);
  });

  function setHeights(scrollHeight: number, clientHeight: number) {
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
      configurable: true,
      get: () => scrollHeight,
    });
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
      configurable: true,
      get: () => clientHeight,
    });
  }
  // Clamped: контент 999, видимая часть 100.
  function forceClamped() { setHeights(999, 100); }
  // Not clamped: контент полностью помещается.
  function forceNotClamped() { setHeights(50, 100); }

  it('always shows the text', () => {
    forceNotClamped();
    render(<ExpandableText text="Краткий текст." />);
    expect(screen.getByText('Краткий текст.')).toBeInTheDocument();
  });

  it('no "Развернуть" when text fits', () => {
    forceNotClamped();
    render(<ExpandableText text="Короткий." />);
    expect(screen.queryByRole('button', { name: /Развернуть/ })).not.toBeInTheDocument();
  });

  it('shows "Развернуть" when text is clamped, toggles to "Свернуть"', async () => {
    const user = userEvent.setup();
    forceClamped();
    render(<ExpandableText text="Длинный текст в несколько строк." lines={2} />);
    const expandBtn = screen.getByRole('button', { name: 'Развернуть' });
    expect(expandBtn).toBeInTheDocument();

    await user.click(expandBtn);
    expect(screen.getByRole('button', { name: 'Свернуть' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Развернуть' })).not.toBeInTheDocument();
  });
});
