import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

// Редизайн карточки книги (1.3.x): компактная строка сигналов, технические поля
// под раскрывашкой «Детали файла», сворачивание длинной аннотации. jsdom не
// считает CSS-layout (line-clamp, видимость внутри закрытого <details>) —
// поэтому эти проверки только в Playwright (граблю №4).

test('детали файла: свёрнуты по умолчанию, раскрываются по клику', async ({
  mockedPage: page,
}) => {
  await page.goto('/books/19');
  // На десктопе «Детали файла» — в шапке у обложки; на мобайле дублируется ниже
  // (разные раскладки md:block / md:hidden). В e2e-вьюпорте (десктоп) видима
  // первая (десктопная) копия — её и берём.
  const summary = page.getByText('Детали файла').first();
  await expect(summary).toBeVisible({ timeout: 10_000 });

  // Размер (formatBytes(849047) → «829.1 КБ») скрыт пока <details> закрыт.
  const size = page.getByText('829.1 КБ').first();
  await expect(size).toBeHidden();
  await summary.click();
  await expect(size).toBeVisible();

  // Размер в человекочитаемых единицах, не сырых KiB (регрессия редизайна).
  await expect(page.getByText(/KiB/)).toHaveCount(0);
});

test('строка сигналов: внешний рейтинг + источник в тултипе', async ({ mockedPage: page }) => {
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      // rating (LIBRATE) → externalRatingDisplay → «4.2 · библиотека».
      body: JSON.stringify({ ...bookDetailFixture, rating: 4.2 }),
    }),
  );

  await page.goto('/books/19');
  const rating = page.getByText('4.2', { exact: true });
  await expect(rating).toBeVisible({ timeout: 10_000 });

  // Источник — в тултипе по ховеру (Globe-чип), не текстом рядом.
  await rating.hover();
  await expect(page.getByText('Внешний рейтинг · библиотека')).toBeVisible({ timeout: 5_000 });
});

test('строка сигналов: оценка Фантлаба со счётом оценок в тултипе (#296)', async ({
  mockedPage: page,
}) => {
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, fantlab_rating: 8.97, fantlab_marks: 11889 }),
    }),
  );
  await page.goto('/books/19');
  const chip = page.getByText('Фантлаб', { exact: true });
  await expect(chip).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText('9.0', { exact: true })).toBeVisible();
  await chip.hover();
  await expect(page.getByText(/Средняя оценка на fantlab\.ru \(из 10\) · 11889 оценок/)).toBeVisible({ timeout: 5_000 });
});

test('аннотация: длинная сворачивается, «Развернуть» раскрывает', async ({
  mockedPage: page,
}) => {
  const longAnnotation = Array.from(
    { length: 30 },
    (_, i) =>
      `Параграф номер ${i + 1} с достаточно длинным предложением, чтобы текст ` +
      `гарантированно переносился на несколько строк в карточке книги.`,
  ).join(' ');

  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, annotation: longAnnotation }),
    }),
  );

  await page.goto('/books/19');
  // Длинный текст обрезан по строкам → видна кнопка «Развернуть».
  const expand = page.getByRole('button', { name: 'Развернуть' });
  await expect(expand).toBeVisible({ timeout: 10_000 });
  await expand.click();
  await expect(page.getByRole('button', { name: 'Свернуть' })).toBeVisible();
});

// #443: на мобиле служебные поля (серия, жанры) — во всю ширину под обложкой,
// а не узкой колонкой вдвое выше обложки с пустотой слева.
test('карточка на мобиле: жанры под обложкой во всю ширину', async ({ mockedPage: page }) => {
  const genres = [
    ...bookDetailFixture.genres,
    ...['sf_social', 'sf_space', 'prose_classic', 'sf_horror', 'sf_cyberpunk'].map((code, i) => ({ id: 10 + i, code, display: code })),
  ];
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...bookDetailFixture, genres }) }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/books/19');
  const cover = page.locator('[class*="aspect-[2/3]"]').first();
  const chip = page.getByText('sf_cyberpunk', { exact: true });
  await expect(chip).toBeVisible({ timeout: 10_000 });
  const c = await cover.boundingBox();
  const g = await chip.locator('xpath=..').boundingBox();
  expect(c && g).toBeTruthy();
  expect(g!.y).toBeGreaterThanOrEqual(c!.y + c!.height - 1); // жанры начинаются под обложкой
  expect(g!.x).toBeLessThanOrEqual(c!.x + 1); // и от её левого края
});

// #470: действия на мобиле — две ровные строки во всю ширину: «Читать» + ★
// квадратом той же высоты; ниже — равные «Скачать · Kindle · На полку». Края строк
// совпадают, ничего не торчит за кнопку и за экран — на узких телефонах тоже.
for (const width of [320, 360, 414]) {
  test(`карточка на мобиле: ровные строки действий (${width}px)`, async ({ mockedPage: page }) => {
    await page.route(/\/api\/me\/kindle-targets$/, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          items: [
            { id: 1, label: 'Paperwhite', email: 'a@kindle.com' },
            { id: 2, label: 'Oasis', email: 'b@kindle.com' },
          ],
        }),
      }),
    );
    await page.route(/\/api\/books\/19\/collections$/, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ items: [{ id: 1, name: 'Отпуск', kind: 'user' }, { id: 2, name: 'Подарить', kind: 'user' }] }),
      }),
    );
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/books/19');
    const read = page.getByRole('link', { name: 'Открыть книгу в браузерном ридере' }).last();
    const star = page.getByRole('button', { name: /избранное/i }).last();
    const download = page.getByRole('button', { name: 'Скачать' }).last();
    const kindle = page.getByRole('button', { name: 'Отправить на Kindle' }).last();
    const shelf = page.getByRole('button', { name: /На полках: 2/ });
    await expect(shelf).toBeVisible({ timeout: 10_000 });
    const [r, s, d, k, b] = await Promise.all([read, star, download, kindle, shelf].map((l) => l.boundingBox()));
    expect(r && s && d && k && b).toBeTruthy();
    expect(Math.abs(r!.y - s!.y)).toBeLessThan(2); // «Читать» и ★ — одна строка
    expect(Math.abs(r!.height - s!.height)).toBeLessThan(1); // одной высоты
    expect(Math.abs(d!.y - b!.y)).toBeLessThan(2); // три кнопки — одна строка
    expect(Math.abs(d!.width - b!.width)).toBeLessThan(2); // равной ширины
    expect(Math.abs(r!.x - d!.x)).toBeLessThan(1); // левые края строк совпадают
    expect(Math.abs(s!.x + s!.width - (b!.x + b!.width))).toBeLessThan(1); // и правые
    expect(b!.x + b!.width).toBeLessThanOrEqual(width);
    // Содержимое не вылезает за кнопку (счётчик полок, стрелка Kindle).
    for (const l of [download, kindle, shelf]) {
      expect(await l.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
    }
  });
}

// #612: у работы с несколькими изданиями в ряду действий были три подписи
// («В избранное · Хочу прочитать · На полку») — на телефоне не помещались и
// налезали друг на друга; длинный чип серии издания растягивал страницу вбок.
for (const width of [320, 390, 402]) {
  test(`карточка на мобиле: несколько изданий — кнопки не налезают, страница не шире экрана (${width}px)`, async ({
    mockedPage: page,
  }) => {
    const edition = { lang: 'ru', size_bytes: 1, ext: 'fb2', archive: 'a.zip' };
    const multi = {
      ...bookDetailFixture,
      work_id: 500,
      editions: [
        { ...edition, id: 19, file_name: '1' },
        {
          ...edition,
          id: 20,
          file_name: '2',
          edition_title: 'Собрание сочинений в пяти томах. Том 5',
          series: { id: 7, title: 'Булгаков М.А. Собрание сочинений в 5 т.' },
        },
      ],
    };
    await page.route(/\/api\/books\/19$/, (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(multi) }),
    );
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/books/19');
    const chip = page.getByText('Серия: Булгаков М.А. Собрание сочинений в 5 т.');
    await expect(chip).toBeVisible({ timeout: 10_000 });

    const shelf = page.getByRole('button', { name: 'Добавить на полку' }).last();
    const toRead = page.getByRole('button', { name: 'Добавить в «Хочу прочитать»' }).last();
    const star = page.getByRole('button', { name: /избранное/i }).last();
    const boxes = await Promise.all([shelf, toRead, star].map((l) => l.boundingBox()));
    expect(boxes.every(Boolean)).toBe(true);
    const row = boxes.map((x) => x!).sort((a, c) => a.x - c.x);
    for (let i = 1; i < row.length; i++) {
      expect(Math.abs(row[i].y - row[0].y)).toBeLessThan(2); // одна строка
      expect(row[i - 1].x + row[i - 1].width).toBeLessThanOrEqual(row[i].x); // не налезают
    }
    expect(row[row.length - 1].x + row[row.length - 1].width).toBeLessThanOrEqual(width);
    // Подпись не вылезает за свою кнопку.
    for (const l of [shelf, toRead, star]) {
      expect(await l.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
    }
    const c = await chip.boundingBox();
    expect(c!.x + c!.width).toBeLessThanOrEqual(width);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
  });
}

test('карточка на мобиле: книга на полках — «Полки N» в ряду действий, чипы ниже', async ({ mockedPage: page }) => {
  await page.route(/\/api\/books\/19\/collections$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          { id: 1, name: 'Отпуск', kind: 'manual' },
          { id: 2, name: 'Подарить', kind: 'manual' },
          { id: 3, name: 'Избранное', kind: 'favorites' },
        ],
      }),
    }),
  );
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('/books/19');
  const shelf = page.getByRole('button', { name: /На полках: 2/ });
  await expect(shelf).toBeVisible({ timeout: 10_000 });
  // Чип есть и в скрытом десктопном блоке — берём видимый.
  await expect(page.locator('text="Подарить" >> visible=true')).toHaveCount(1);
  await expect(page.getByRole('button', { name: 'Добавить на полку' })).toHaveCount(0);
});
