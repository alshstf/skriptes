import type { Page } from '@playwright/test';
import { test, expect } from './_fixtures';

// Блок «ИИ» на странице «Общее» админки: без ключа — одна подсказка; с ключом —
// выключатель (сразу), потолок расходов (под SaveBar) и расход за месяц.

const base = {
  configured: true,
  model: 'claude-sonnet-5-5',
  fast_model: 'claude-haiku-5-5',
  daily_per_user: 20,
  budget_usd: 10,
  spent_usd: 0.42,
  by_feature: [
    { feature: 'explain', requests: 14, actions: 11, usd: 0.4 },
    { feature: 'translate', requests: 6, actions: 6, usd: 0.02 },
  ],
  disabled: false,
  disabled_users: [],
  max_budget_usd: 1000,
};

async function mockPage(page: Page, ai: Record<string, unknown>) {
  const puts: Record<string, unknown>[] = [];
  let current = { ...ai };
  await page.route(/\/api\/admin\/branding$/, (r) =>
    r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ instance_name: 'skriptes' }) }),
  );
  await page.route(/\/api\/admin\/search-log\/misses/, (r) =>
    r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], days: 90 }) }),
  );
  await page.route(/\/api\/admin\/ai$/, (r) => {
    if (r.request().method() === 'PUT') {
      const body = r.request().postDataJSON();
      puts.push(body);
      current = { ...current, disabled: body.disabled, budget_usd: body.monthly_budget_usd ?? 10 };
    }
    return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(current) });
  });
  return puts;
}

test('админка, ИИ: без ключа — подсказка, без контролов', async ({ mockedPage: page }) => {
  await mockPage(page, { ...base, configured: false, by_feature: [], spent_usd: 0 });
  await page.goto('/admin/general');
  await expect(page.getByText(/не задан ключ \(SKRIPTES_LLM_API_KEY\)/)).toBeVisible();
  await expect(page.getByLabel('Потолок расходов в месяц, $')).toHaveCount(0);
  await expect(page.getByRole('switch')).toHaveCount(0);
});

test('админка, ИИ: расход, потолок под SaveBar, выключатель сразу', async ({ mockedPage: page }) => {
  const puts = await mockPage(page, base);
  await page.goto('/admin/general');
  await expect(page.getByText('В этом месяце:')).toContainText('0,42 $ из 10,00 $, осталось 9,58 $');
  await expect(page.getByRole('row', { name: /Объяснить/ })).toContainText('11');
  await expect(page.getByRole('row', { name: /Перевести/ })).toContainText('0,02 $');
  if (process.env.SHOT) await page.screenshot({ path: process.env.SHOT, fullPage: true });

  // Потолок: мусор не сохраняется, число — через SaveBar.
  const budget = page.getByLabel('Потолок расходов в месяц, $');
  await budget.fill('много');
  await expect(page.getByRole('button', { name: 'Сохранить' })).toHaveCount(0);
  await budget.fill('3,5');
  await page.getByRole('button', { name: 'Сохранить' }).click();
  await expect.poll(() => puts.length).toBe(1);
  expect(puts[0]).toEqual({ monthly_budget_usd: 3.5, disabled: false, disabled_users: [] });
  await expect(page.getByText('В этом месяце:')).toContainText('из 3,50 $');

  // Выключатель — сразу, без SaveBar.
  await page.getByRole('switch', { name: 'ИИ-функции включены' }).click();
  await expect.poll(() => puts.length).toBe(2);
  expect(puts[1]).toMatchObject({ disabled: true, monthly_budget_usd: 3.5 });
  await expect(page.getByRole('switch', { name: 'ИИ-функции включены' })).not.toBeChecked();
});
