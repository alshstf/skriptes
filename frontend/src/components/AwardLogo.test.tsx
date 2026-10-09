import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { AwardLogo } from './AwardLogo';
import { monogram } from '@/lib/awards';

describe('AwardLogo (#446)', () => {
  it('монограмма — первые буквы значимых слов', () => {
    expect(monogram('Меч без имени')).toBe('МБИ');
    expect(monogram('Итоги года «Мира фантастики»')).toBe('МФ');
    expect(monogram('Премия Андрея Белого')).toBe('АБ');
    expect(monogram('Хьюго')).toBe('Х');
  });

  it('есть картинка — img из кэша; не загрузилась — монограмма', () => {
    const { container } = render(<AwardLogo award={{ key: 'hugo', name: 'Хьюго', logo: true }} />);
    const img = container.querySelector('img');
    expect(img?.getAttribute('src')).toBe('/api/awards/hugo/logo');
    fireEvent.error(img!);
    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('Х')).toBeTruthy();
  });

  it('картинки нет — сразу монограмма', () => {
    const { container } = render(<AwardLogo award={{ key: 'sword-without-name', name: 'Меч без имени' }} />);
    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('МБИ')).toBeTruthy();
  });
});
