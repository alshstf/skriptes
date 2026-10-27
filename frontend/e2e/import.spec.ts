import { test, expect } from './_fixtures';

// Импорт истории чтения (recsys/04, 4.4): файл → предпросмотр сопоставления →
// уверенные с галочками, спорные — выбор, ненайденные — справкой → применить →
// прошлый импорт можно отменить. Под CSP (e2e на собранном билде).

const preview = {
  source: 'goodreads',
  file_name: 'goodreads_library_export.csv',
  items: [
    { i: 0, title: 'Roadside Picnic', authors: ['Arkady Strugatsky'], rating: 5, read: true, read_at: '2024-03-15T00:00:00Z',
      shelf: 'read', highlights: 0, line: 2,
      match: { confidence: 'high', work_id: 1001, candidates: [{ work_id: 1001, title: 'Пикник на обочине', authors: ['Стругацкий Аркадий'], year: 1972 }] } },
    { i: 1, title: 'Мы', authors: ['Евгений Замятин'], rating: 4, read: true, shelf: 'read', highlights: 0, line: 3,
      match: { confidence: 'high', work_id: 1002, candidates: [{ work_id: 1002, title: 'Мы', authors: ['Замятин Евгений'] }] } },
    { i: 2, title: 'Детство', authors: ['Алексей Толстой'], read: false, shelf: 'to_read', highlights: 0, line: 4,
      match: { confidence: 'medium', candidates: [
        { work_id: 1003, title: 'Детство', authors: ['Толстой Лев'], year: 1852 },
        { work_id: 1004, title: 'Детство Никиты', authors: ['Толстой Алексей'], year: 1922 },
      ] } },
    { i: 3, title: 'The Name of the Wind', authors: ['Patrick Rothfuss'], read: false, shelf: 'to_read', highlights: 0, line: 5,
      match: { confidence: 'none' } },
  ],
  summary: { high: 2, medium: 1, none: 1 },
  skipped: [{ line: 6, reason: 'нет названия' }],
  skipped_total: 1,
};

test('импорт: файл → выбор → применить → отменить', async ({ mockedPage: page }) => {
  let applied: { choices: unknown; watch: string; source: string; hasFile: boolean } | null = null;
  let undone = false;
  const batch = () => ({
    batch_id: 7, source: 'goodreads', file_name: 'goodreads_library_export.csv', items: 4, applied: 2, ratings: 1,
    reads: 1, to_read: 0, highlights: 0, kept: 1, applied_at: new Date().toISOString(),
    ...(undone ? { undone_at: new Date().toISOString() } : {}),
  });
  await page.route(/\/api\/me\/import\/preview$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(preview) }),
  );
  await page.route(/\/api\/me\/import\/apply$/, (route) => {
    const body = route.request().postData() ?? '';
    const field = (name: string) => body.match(new RegExp(`name="${name}"\\r\\n\\r\\n([^\\r]*)`))?.[1] ?? '';
    applied = {
      choices: JSON.parse(field('choices')), watch: field('watch'), source: field('source'),
      hasFile: body.includes('filename="goodreads_library_export.csv"'),
    };
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ batch_id: 7, applied: 2, ratings: 1, reads: 1, to_read: 0, highlights: 0, kept: 1, wishlist: 1 }) });
  });
  await page.route(/\/api\/me\/import\/batches$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: applied ? [batch()] : [] }) }),
  );
  await page.route(/\/api\/me\/import\/batches\/7$/, (route) => {
    undone = true;
    return route.fulfill({ status: 204 });
  });

  await page.goto('/me/import');
  await expect(page.getByRole('link', { name: 'Импорт' })).toBeVisible();
  await expect(page.getByRole('link', { name: /Скачать шаблон/ })).toHaveAttribute('href', '/api/me/import/template.csv');
  await expect(page.getByRole('link', { name: 'ваша история из этой библиотеки' })).toHaveAttribute(
    'href',
    '/api/me/import/export.csv',
  );

  await page.locator('input[type=file]').setInputFiles({
    name: 'goodreads_library_export.csv', mimeType: 'text/csv', buffer: Buffer.from('Title,My Rating\nX,5\n'),
  });
  await page.getByRole('button', { name: 'Разобрать и сопоставить' }).click();

  await expect(page.getByTestId('import-summary')).toContainText('уверенно — 2 · на выбор — 1 · не нашлось — 1 · пропущено строк — 1');
  const high = page.getByTestId('import-high');
  await expect(high).toContainText('Пикник на обочине — Стругацкий Аркадий, 1972');
  await expect(high).toContainText('оценка 5 · прочитано 15.03.2024');
  // Спорная — пока не выбрана: применяются только две уверенные.
  const apply = page.getByRole('button', { name: /^Применить/ });
  await expect(apply).toHaveText('Применить — 2 книги');

  // «Мы» — не та книга: снимаем галочку; «Детство» — выбираем Алексея Толстого.
  await high.getByRole('checkbox').nth(1).uncheck();
  const medium = page.getByTestId('import-medium');
  await medium.getByRole('radio', { name: /Детство Никиты/ }).check();
  await expect(apply).toHaveText('Применить — 2 книги');
  await page.getByText('Нет в библиотеке — 1').click();
  await expect(page.getByTestId('import-none')).toContainText('The Name of the Wind');
  // Ненайденные — под наблюдением по умолчанию (#597).
  await expect(page.getByRole('checkbox', { name: /Следить за ненайденными/ })).toBeChecked();

  await apply.click();
  await expect.poll(() => applied).not.toBeNull();
  expect(applied!.choices).toEqual([{ i: 0, work_id: 1001 }, { i: 2, work_id: 1004 }]);
  expect(applied!.watch).toBe('[3]');
  expect(applied!.source).toBe('goodreads');
  expect(applied!.hasFile).toBe(true);
  await expect(page.getByText('Импортировано 2 книги.')).toBeVisible();
  await expect(page.getByText('оценок — 1 · прочитано — 1 · ждём появления — 1 · уже было своё — 1').first()).toBeVisible();
  await expect(page.getByTestId('import-summary')).toHaveCount(0);

  const batches = page.getByTestId('import-batches');
  await expect(batches).toContainText('Goodreads · goodreads_library_export.csv');
  await batches.getByRole('button', { name: 'Отменить' }).click();
  await expect(batches).toContainText('То, что вы потом меняли сами, останется.');
  await batches.getByRole('button', { name: 'Отменить импорт' }).click();
  await expect(batches).toContainText('отменён');
  await expect(batches.getByRole('button', { name: 'Отменить' })).toHaveCount(0);
});

test('импорт: понятная ошибка формата', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/import\/batches$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  await page.route(/\/api\/me\/import\/preview$/, (route) =>
    route.fulfill({ status: 422, contentType: 'application/json', body: JSON.stringify({ error: 'формат файла не распознан: нужен My Clippings.txt с Kindle, CSV-каталог calibre, экспорт Goodreads или CSV по шаблону' }) }),
  );
  await page.goto('/me/import');
  await page.locator('input[type=file]').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('просто текст') });
  await page.getByRole('button', { name: 'Разобрать и сопоставить' }).click();
  await expect(page.getByRole('alert')).toContainText('формат файла не распознан');
});

test.describe('телефон', () => {
  test.use({ viewport: { width: 390, height: 844 } });
  test('вкладки профиля не тянут страницу вбок, активная — на виду', async ({ mockedPage: page }) => {
    await page.route(/\/api\/me\/import\/batches$/, (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
    );
    await page.goto('/me/import');
    await expect(page.getByRole('heading', { name: 'Импорт истории чтения' })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    const tab = await page.getByRole('link', { name: 'Импорт' }).boundingBox();
    expect(tab!.x + tab!.width).toBeLessThanOrEqual(390);
  });
});

// #597: в файле только книги, которых нет в библиотеке, — их можно взять под
// наблюдение без применения; без галочки применять нечего.
test('импорт: только ненайденные — «Следить»', async ({ mockedPage: page }) => {
  let watch = '';
  const onlyMissing = { ...preview, items: [preview.items[3]], summary: { high: 0, medium: 0, none: 1 }, skipped: [], skipped_total: 0 };
  await page.route(/\/api\/me\/import\/preview$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(onlyMissing) }),
  );
  await page.route(/\/api\/me\/import\/apply$/, (route) => {
    const body = route.request().postData() ?? '';
    watch = body.match(/name="watch"\r\n\r\n([^\r]*)/)?.[1] ?? '';
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ batch_id: 8, applied: 0, ratings: 0, reads: 0, to_read: 0, highlights: 0, kept: 0, wishlist: 1 }) });
  });
  await page.route(/\/api\/me\/import\/batches$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }),
  );
  await page.goto('/me/import');
  await page.locator('input[type=file]').setInputFiles({
    name: 'goodreads_library_export.csv', mimeType: 'text/csv', buffer: Buffer.from('Title\nX\n'),
  });
  await page.getByRole('button', { name: 'Разобрать и сопоставить' }).click();
  const go = page.getByRole('button', { name: /^Следить/ });
  await expect(go).toHaveText('Следить — 1 книга');
  const box = page.getByRole('checkbox', { name: /Следить за ненайденными/ });
  await box.uncheck();
  await expect(go).toBeDisabled();
  await box.check();
  await go.click();
  await expect.poll(() => watch).toBe('[3]'); // индекс книги в файле
  await expect(page.getByText('ждём появления — 1')).toBeVisible();
});
