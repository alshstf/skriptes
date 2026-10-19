import { test, expect, mockApi, bookDetailFixture } from './_fixtures';

// Ручные merge/split + админ-подсказки. Admin-гейт и поведение завязаны на роль
// и на клиентскую группировку по ser_no — проверяем в Playwright (реальный DOM).

// Серия с дублем тома #7 (две разные работы под одним ser_no) — кейс «Страйка».
const seriesWithDup = {
  id: 7,
  title: 'Корморан Страйк',
  author_id: 17,
  author_name: 'Гэлбрейт Роберт',
  book_count: 2,
  books: [
    {
      id: 14,
      work_id: 269666,
      title: 'Развороченная могила',
      authors: ['Гэлбрейт Роберт'],
      series: 'Корморан Страйк',
      series_id: 7,
      ser_no: 7,
      lib_id: 'L1',
    },
    {
      id: 505134,
      work_id: 277728,
      title: 'Неизбежная могила',
      authors: ['Гэлбрейт Роберт'],
      series: 'Корморан Страйк',
      series_id: 7,
      ser_no: 7,
      lib_id: 'L2',
    },
  ],
  is_favorite: false,
  year_stats: [],
  read_count: 0,
};

function asUser(role: 'admin' | 'user') {
  return JSON.stringify({
    user: {
      id: 1,
      email: 'tester@example.com',
      display_name: 'Tester',
      role,
      created_at: '2026-05-10T00:00:00Z',
    },
  });
}

test('merge suggestion: admin видит плашку и «Объединить» шлёт work_ids', async ({ page }) => {
  await mockApi(page); // userFixture = admin по умолчанию
  await page.route(/\/api\/series\/7$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(seriesWithDup) }),
  );
  const calls: Array<{ work_ids: number[] }> = [];
  await page.route(/\/api\/admin\/works\/merge$/, (route) => {
    calls.push(route.request().postDataJSON());
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ work_id: 269666 }),
    });
  });

  await page.goto('/series/7');
  await expect(page.getByText(/Похоже, том #7/)).toBeVisible({ timeout: 10_000 });
  await page.getByRole('button', { name: 'Посмотреть' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByText('«Неизбежная могила»')).toBeVisible();
  await dialog.getByRole('button', { name: 'Объединить', exact: true }).click();

  await expect.poll(() => calls.length).toBe(1);
  expect([...calls[0].work_ids].sort((a, b) => a - b)).toEqual([269666, 277728]);
  // Решённая группа уходит сразу, вместе с плашкой.
  await expect(page.getByTestId('merge-suggestions')).toHaveCount(0);
});

// #517: у серии с многими нерасклеенными томами — ОДНА сводная плашка (не стена
// подсказок), кнопка жёстко справа от текста; «Не объединять» запоминает отказ, а
// с отказом от сервера (merge_rejections) подсказки нет.
test('merge suggestions: одна сводная плашка, окно со всеми томами, «не объединять»', async ({ page }) => {
  await mockApi(page);
  const books = Array.from({ length: 6 }, (_, i) => [
    { id: 1000 + i * 2, work_id: 2000 + i * 2, title: `Том ${i + 1}: очень длинное название первого издания`, ser_no: i + 1 },
    { id: 1001 + i * 2, work_id: 2001 + i * 2, title: `Том ${i + 1}: другое название второго издания`, ser_no: i + 1 },
  ])
    .flat()
    .map((b) => ({ ...b, authors: ['Гэлбрейт Роберт'], series: 'Механизмы радости', series_id: 8, lib_id: `L${b.id}` }));
  let rejections: [number, number][] = [];
  await page.route(/\/api\/series\/8$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...seriesWithDup, id: 8, title: 'Механизмы радости', book_count: 12, books, merge_rejections: rejections }),
    }),
  );
  const rejected: Array<{ work_ids: number[] }> = [];
  await page.route(/\/api\/admin\/works\/merge-rejections$/, (route) => {
    const body = route.request().postDataJSON() as { work_ids: number[] };
    rejected.push(body);
    rejections = [...rejections, [body.work_ids[0], body.work_ids[1]]];
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ pairs: 1 }) });
  });

  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/series/8');
  const plaque = page.getByTestId('merge-suggestions');
  await expect(plaque).toHaveCount(1, { timeout: 10_000 });
  await expect(plaque).toContainText('Похоже, 6 томов серии изданы в нескольких вариантах');
  // Кнопка — справа от текста, в первой строке (не переносится под текст).
  const text = (await plaque.locator('span').first().boundingBox())!;
  const btn = (await plaque.getByRole('button', { name: 'Посмотреть' }).boundingBox())!;
  expect(btn.x).toBeGreaterThan(text.x + text.width - 1);
  expect(btn.y).toBeLessThan(text.y + 8);

  await plaque.getByRole('button', { name: 'Посмотреть' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByTestId('merge-group')).toHaveCount(6);
  await dialog.getByTestId('merge-group').first().getByRole('button', { name: 'Не объединять' }).click();
  await expect.poll(() => rejected.length).toBe(1);
  expect([...rejected[0].work_ids].sort((a, b) => a - b)).toEqual([2000, 2001]);
  await expect(dialog.getByTestId('merge-group')).toHaveCount(5);
  await page.keyboard.press('Escape');
  await expect(plaque).toContainText('Похоже, 5 томов');

  // После перезагрузки отказ приходит с сервера — том #1 не предлагается.
  await page.reload();
  await expect(page.getByTestId('merge-suggestions')).toContainText('Похоже, 5 томов', { timeout: 10_000 });

  // На узком экране кнопка всё так же справа.
  await page.setViewportSize({ width: 375, height: 812 });
  const text2 = (await plaque.locator('span').first().boundingBox())!;
  const btn2 = (await plaque.getByRole('button', { name: 'Посмотреть' }).boundingBox())!;
  expect(btn2.x).toBeGreaterThan(text2.x + text2.width - 1);
});

test('merge suggestion: не-админ ничего не видит', async ({ page }) => {
  await mockApi(page);
  // Переопределяем роль ПОСЛЕ mockApi (последний route выигрывает).
  await page.route(/\/api\/auth\/me$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: asUser('user') }),
  );
  await page.route(/\/api\/series\/7$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(seriesWithDup) }),
  );

  await page.goto('/series/7');
  await expect(page.getByText('Развороченная могила')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(/Похоже, том #7/)).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Объединить издания/ })).toHaveCount(0);
});

test('split: admin выносит издание в отдельную книгу', async ({ page }) => {
  await mockApi(page);
  const multi = {
    ...bookDetailFixture,
    work_id: 500,
    editions: [
      // #19 — якорное (название = названию работы), залочено, выносить нельзя.
      { id: 19, is_anchor: true, lang: 'ru', translator: 'Вебер Виктор', size_bytes: 849047, ext: 'fb2', archive: 'a.zip', file_name: '749080' },
      // #20 — единственный не-якорь («чужая» книга) → выносится по подтверждению.
      { id: 20, title: 'Чужая книга (ошибочно слита)', lang: 'en', size_bytes: 700000, ext: 'fb2', archive: 'b.zip', file_name: '749081' },
    ],
  };
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(multi) }),
  );
  const calls: Array<{ book_ids: number[] }> = [];
  await page.route(/\/api\/admin\/works\/split$/, (route) => {
    calls.push(route.request().postDataJSON());
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ work_id: 999 }),
    });
  });

  await page.goto('/books/19');
  await page.getByRole('button', { name: 'Править', exact: true }).click(); // режим правки (#444)
  await page.getByRole('button', { name: 'Разделить', exact: true }).click();
  // 2 издания: якорь залочен, второй — единственный не-якорь → выбора нет,
  // только подтверждение «Вынести». Скоупим на диалог (название «чужой» книги
  // есть и в строке издания на карточке).
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByText(/Якорное — остаётся/)).toBeVisible();
  await expect(dialog.getByText(/Чужая книга/)).toBeVisible();
  await dialog.getByRole('button', { name: /Вынести/ }).click();

  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0].book_ids).toEqual([20]);
});

test('merge с поиском: admin присоединяет найденную книгу (target = текущая)', async ({ page }) => {
  await mockApi(page);
  // Карточка с work_id → кнопка «Объединить с другой книгой…» доступна админу.
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, work_id: 500 }),
    }),
  );
  // Typeahead отдаёт работу-цель (id/work_id 777 ≠ 500).
  await page.route(/\/api\/search\/suggest(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        query: 'мог',
        books: [
          {
            id: 777,
            work_id: 777,
            title: 'Та же книга, другое издание',
            authors: ['Гэлбрейт Роберт'],
            year: 2020,
            lib_id: 'X',
          },
        ],
        authors: [],
        series: [],
      }),
    }),
  );
  const calls: Array<{ work_ids: number[]; target?: number }> = [];
  await page.route(/\/api\/admin\/works\/merge$/, (route) => {
    calls.push(route.request().postDataJSON());
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ work_id: 500 }),
    });
  });

  await page.goto('/books/19');
  await page.getByRole('button', { name: 'Править', exact: true }).click(); // режим правки (#444)
  await page.getByRole('button', { name: /Объединить с другой книгой/ }).click();
  await page.getByRole('textbox', { name: /Поиск книги для объединения/ }).fill('мог');
  await page.getByRole('button', { name: /Та же книга, другое издание/ }).click();

  await expect.poll(() => calls.length).toBe(1);
  expect([...calls[0].work_ids].sort((a, b) => a - b)).toEqual([500, 777]);
  expect(calls[0].target).toBe(500); // текущая работа выживает — URL карточки не ломается
});

test('режим правки (#444): без «Править» админ видит карточку как читатель', async ({ page }) => {
  await mockApi(page);
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, work_id: 500 }),
    }),
  );
  await page.goto('/books/19');
  await expect(page.getByRole('button', { name: 'Править', exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: /Объединить с другой книгой/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Править', exact: true }).click();
  await expect(page.getByRole('button', { name: /Объединить с другой книгой/ })).toBeVisible();
  // Перезагрузка той же карточки режим сохраняет.
  await page.reload();
  await expect(page.getByRole('button', { name: 'Готово', exact: true })).toBeVisible({ timeout: 10_000 });
  // Режим — у карточки, где его включили (владелец 2026-10-19): перешли на
  // другую карточку внутри приложения — выключен; вернулись «Назад» — тоже.
  await page.getByRole('link', { name: 'Алексеев Евгений Артёмович' }).first().click();
  await expect(page).toHaveURL(/\/authors\/17$/);
  await expect(page.getByRole('button', { name: 'Править', exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: 'Готово', exact: true })).toHaveCount(0);
  await page.goBack();
  await expect(page).toHaveURL(/\/books\/19$/);
  await expect(page.getByRole('button', { name: 'Править', exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: /Объединить с другой книгой/ })).toHaveCount(0);
  // «Готово» выключает режим и на месте.
  await page.getByRole('button', { name: 'Править', exact: true }).click();
  await page.getByRole('button', { name: 'Готово', exact: true }).click();
  await expect(page.getByRole('button', { name: /Объединить с другой книгой/ })).toHaveCount(0);
});
