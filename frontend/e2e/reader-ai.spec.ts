import type { Page } from '@playwright/test';
import { test, expect } from './_fixtures';
import { BOOK_ID, chapterDoc, location, openReader, stubReader } from './_reader';

// ИИ-функции читалки: кнопки есть, только когда сервер говорит, что ИИ доступен;
// вопрос уходит по явному действию читателя; ответ приходит потоком; по цитате
// из ответа ридер переходит к месту в книге.

function sse(...events: Record<string, unknown>[]): string {
  return events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join('');
}

async function mockAi(page: Page, opts: { available?: boolean; reply?: string; status?: number; json?: unknown } = {}) {
  const asked: Record<string, unknown>[] = [];
  await page.route(/\/api\/ai\/status$/, (r) =>
    r.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(
        opts.available === false ? { available: false, reason: 'no_key', daily_left: 0 } : { available: true, daily_left: 20 },
      ),
    }),
  );
  await page.route(new RegExp(`/api/books/${BOOK_ID}/ask$`), (r) => {
    asked.push(r.request().postDataJSON());
    if (opts.status) {
      return r.fulfill({ status: opts.status, contentType: 'application/json', body: JSON.stringify(opts.json ?? {}) });
    }
    return r.fulfill({
      status: 200,
      contentType: 'text/event-stream; charset=utf-8',
      body:
        opts.reply ??
        sse(
          { kind: 'stage', stage: 1 },
          { kind: 'delta', text: 'Черновик, который модель бросила.' },
          { kind: 'stage', stage: 2 },
          { kind: 'delta', text: 'Он идёт по пустыне, ' },
          { kind: 'delta', text: 'потому что так начинается вторая глава.' },
          { kind: 'done', quotes: [{ text: '2.5. Он шёл по пустыне', section: 1 }] },
        ),
    });
  });
  return asked;
}

async function selectInChapter(page: Page, n: number, paragraph: number, chars: number) {
  const doc = await chapterDoc(page, n);
  await doc.evaluate(
    ({ id, chars }) => {
      const p = document.getElementById(id)!;
      const node = p.firstChild!;
      const range = document.createRange();
      range.setStart(node, 0);
      range.setEnd(node, chars);
      const sel = document.getSelection()!;
      sel.removeAllRanges();
      sel.addRange(range);
      document.dispatchEvent(new PointerEvent('pointerup', { bubbles: true }));
    },
    { id: `p${n}-${paragraph}`, chars },
  );
}

test('ридер, ИИ: без ключа кнопок нет', async ({ mockedPage: page }) => {
  await stubReader(page);
  await mockAi(page, { available: false });
  await openReader(page);
  await selectInChapter(page, 1, 2, 22);
  const bar = page.getByRole('toolbar', { name: 'Выделенный текст' });
  await expect(bar.getByRole('button', { name: 'Выделить' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Спросить ИИ' })).toHaveCount(0);
});

test('ридер, ИИ: выделение → «Объяснить» → ответ потоком → переход по цитате', async ({ mockedPage: page }) => {
  await stubReader(page);
  const asked = await mockAi(page);
  await openReader(page);
  await selectInChapter(page, 1, 2, 22);

  await page.getByRole('toolbar', { name: 'Выделенный текст' }).getByRole('button', { name: 'Спросить ИИ' }).click();
  const sheet = page.getByRole('dialog', { name: 'Спросить ИИ' });
  await expect(sheet.getByText('«1.2. Он шёл по пустыне')).toBeVisible();
  expect(asked, 'пока читатель не выбрал действие — запросов нет').toHaveLength(0);

  await sheet.getByRole('button', { name: 'Объяснить' }).click();
  await expect(sheet.getByText('Он идёт по пустыне, потому что так начинается вторая глава.')).toBeVisible();
  await expect(sheet.getByText('Черновик, который модель бросила.'), 'текст брошенной ступени не показываем').toHaveCount(0);
  expect(asked).toHaveLength(1);
  expect(asked[0]).toMatchObject({ mode: 'explain', section: 0, excerpt: '1.2. Он шёл по пустыне', stage: 0, thread: [] });
  expect(asked[0].hint as number).toBeGreaterThan(0);
  expect(asked[0].hint as number).toBeLessThan(0.2);

  // Следующий вопрос — с историей и с достигнутой ступени.
  await sheet.getByLabel('Спросить ещё').fill('А куда он идёт?');
  await sheet.getByRole('button', { name: 'Спросить', exact: true }).click();
  await expect.poll(() => asked.length).toBe(2);
  expect(asked[1]).toMatchObject({ mode: 'explain', question: 'А куда он идёт?', stage: 2 });
  expect((asked[1].thread as unknown[]).length).toBe(2);

  // Цитата из ответа → ридер переходит во вторую главу, шторка закрыта.
  await sheet.getByRole('button', { name: /Показать место: «2\.5\. Он шёл по пустыне»/ }).first().click();
  await expect(sheet).toHaveCount(0);
  await expect.poll(async () => (await location(page))?.index, { timeout: 10_000 }).toBe(1);
  await expect(page.getByRole('button', { name: /Вернуться на прежнее место/ })).toBeVisible();
});

test('ридер, ИИ: «Напомнить, что было» из панели и понятная ошибка', async ({ mockedPage: page }) => {
  await stubReader(page);
  const asked = await mockAi(page, {
    status: 429,
    json: { error: 'ai unavailable', reason: 'quota', message: 'На сегодня вопросы закончились — завтра можно будет спросить снова.' },
  });
  await openReader(page);
  await page.getByRole('button', { name: 'Спросить ИИ' }).click();
  const sheet = page.getByRole('dialog', { name: 'Спросить ИИ' });
  await sheet.getByRole('button', { name: 'Напомнить, что было' }).click();
  await expect(sheet.getByText('На сегодня вопросы закончились — завтра можно будет спросить снова.')).toBeVisible();
  expect(asked[0]).toMatchObject({ mode: 'recap', section: 0 });
  expect(String(asked[0].excerpt)).toContain('Глава 1');
  if (process.env.SHOT) await page.screenshot({ path: process.env.SHOT });
});

test.describe('телефон', () => {
  test.use({ viewport: { width: 390, height: 800 } });
  test('ридер, ИИ: шторка помещается, поле вопроса доступно', async ({ mockedPage: page }) => {
    await stubReader(page);
    await mockAi(page);
    await openReader(page);
    await selectInChapter(page, 1, 2, 22);
    await page.getByRole('toolbar', { name: 'Выделенный текст' }).getByRole('button', { name: 'Спросить ИИ' }).click();
    const sheet = page.getByRole('dialog', { name: 'Спросить ИИ' });
    await sheet.getByRole('button', { name: 'Объяснить' }).click();
    await expect(sheet.getByText(/так начинается вторая глава/)).toBeVisible();
    const box = (await sheet.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
    expect(box.y + box.height).toBeLessThanOrEqual(800);
    await expect(sheet.getByLabel('Спросить ещё')).toBeInViewport();
    if (process.env.SHOT_MOBILE) await page.screenshot({ path: process.env.SHOT_MOBILE });
  });
});
