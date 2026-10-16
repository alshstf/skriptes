import { test, expect, authorDetailFixture } from './_fixtures';
import type { Page } from '@playwright/test';

/**
 * Гистограмма «по годам написания» (#530): у плодовитого автора подписи не
 * налезают друг на друга; «Периоды» собирает годы в корзины; выбор
 * переживает перезагрузку; у редкой гистограммы переключателя нет.
 * Данные — Брэдбери с прода (по работам), layout — только Playwright.
 */
const bradbury =
  '1939:2 1940:2 1941:2 1942:6 1943:9 1944:18 1945:10 1946:22 1947:23 1948:27 1949:16 1950:33 1951:22 1952:11 1953:13 1954:12 1955:6 1956:4 1957:3 1958:6 1959:5 1960:7 1961:3 1962:11 1963:6 1964:3 1966:3 1969:9 1970:1 1971:3 1972:3 1973:3 1975:1 1976:6 1977:1 1978:2 1979:2 1980:2 1981:2 1984:4 1985:4 1987:1 1988:5 1990:3 1991:3 1992:4 1993:1 1994:9 1995:7 1996:15 1997:13 2000:4 2001:4 2002:20 2003:8 2004:3 2005:1 2006:2 2008:4 2009:18 2010:4 2011:1 2012:1 2016:1'
    .split(' ')
    .map((p) => {
      const [year, count] = p.split(':').map(Number);
      return { year, count, books: [{ id: year, work_id: year, title: `Книга ${year}` }] };
    });

async function labelBoxes(page: Page) {
  // Подписи над столбиками — <text> в группе LabelList (оси — отдельные группы).
  return page
    .getByTestId('year-histogram')
    .locator('.recharts-label-list text')
    .evaluateAll((els) => els.map((el) => el.getBoundingClientRect().toJSON() as DOMRect));
}

function expectNoOverlap(boxes: DOMRect[]) {
  const sorted = [...boxes].sort((a, b) => a.left - b.left);
  for (let i = 1; i < sorted.length; i++) {
    const a = sorted[i - 1];
    const b = sorted[i];
    const overlapX = b.left < a.right - 0.5;
    const overlapY = b.top < a.bottom && a.top < b.bottom;
    expect(overlapX && overlapY, `подписи ${i - 1} и ${i} налезают`).toBe(false);
  }
}

test('плотная гистограмма на 375 px: подписи не налезают, «Периоды» — корзины, выбор помнится', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.route(/\/api\/authors\/17$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...authorDetailFixture, year_stats: bradbury }) }),
  );
  await page.goto('/authors/17');
  const hist = page.getByTestId('year-histogram');
  await expect(hist).toHaveAttribute('data-mode', 'years', { timeout: 10_000 });
  await expect(page.getByRole('button', { name: 'Годы', exact: true })).toHaveAttribute('aria-pressed', 'true');
  const yearLabels = await labelBoxes(page);
  expect(yearLabels.length).toBeGreaterThan(3); // пики подписаны
  expect(yearLabels.length).toBeLessThan(bradbury.length); // но не все 64
  expectNoOverlap(yearLabels);

  await page.getByRole('button', { name: 'Периоды', exact: true }).click();
  await expect(hist).toHaveAttribute('data-mode', 'periods');
  await expect(page.getByRole('button', { name: 'Периоды', exact: true })).toHaveAttribute('aria-pressed', 'true');
  const periodLabels = await labelBoxes(page);
  expect(periodLabels.length).toBeGreaterThan(3);
  expect(periodLabels.length).toBeLessThanOrEqual(12); // корзины по 10 лет на телефоне
  expectNoOverlap(periodLabels);

  await page.reload();
  await expect(page.getByTestId('year-histogram')).toHaveAttribute('data-mode', 'periods', { timeout: 10_000 });
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
});

test('редкая гистограмма — без переключателя', async ({ mockedPage: page }) => {
  await page.goto('/authors/17');
  await expect(page.getByTestId('year-histogram')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: 'Периоды', exact: true })).toHaveCount(0);
});
