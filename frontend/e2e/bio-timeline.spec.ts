import { test, expect, mockApi, authorDetailFixture, bookDetailFixture } from './_fixtures';

/**
 * Био-таймлайн (план bio-timeline-v3), layout — только Playwright (jsdom не
 * считает CSS): тизер на карточке автора, блок «В жизни автора в это время»
 * на карточке книги, полная страница с главами и осью, 375 px.
 */

const timeline = {
  available: true,
  qid: 'Q991',
  name: 'Фёдор Достоевский',
  born: '1821-11-11',
  died: '1881-02-09',
  chapters: [
    { key: 'return', title: 'Возвращение, журналы, долги', from: 1860, to: 1866 },
    { key: 'europe', title: 'Анна, Европа, рулетка', from: 1867, to: 1871 },
  ],
  events: [
    { key: 'loss-1864', date: { from: '1864-04-27', precision: 'day' }, type: 'loss', tier: 'major', short: 'Смерть жены Марии Дмитриевны', long: 'Умерла от чахотки.', chapter: 'return' },
    { key: 'poverty-1865', date: { from: '1865-07', precision: 'month' }, type: 'poverty', tier: 'major', short: 'Долги и кабальный договор со Стелловским', long: 'Роман к 1 ноября 1866.', chapter: 'return', works: [{ ref: 'igrok', note: 'под этот договор написан «Игрок»' }] },
    { key: 'career-1861', date: { from: '1861-01', precision: 'month' }, type: 'career', tier: 'minor', short: 'Журнал «Время» вместе с братом', chapter: 'return' },
    { key: 'love-1867', date: { from: '1867-02-27', precision: 'day' }, type: 'love', tier: 'major', short: 'Венчание с Анной Сниткиной', chapter: 'europe' },
    { key: 'relocation-1867', date: { from: '1867-04-26', to: '1871-07-20', precision: 'day' }, type: 'relocation', tier: 'major', short: 'Четыре года в Европе, бегство от кредиторов', chapter: 'europe', works: [{ ref: 'idiot', note: 'роман целиком написан за границей' }] },
    { key: 'loss-1868', date: { from: '1868-05-24', precision: 'day' }, type: 'loss', tier: 'major', short: 'Смерть трёхмесячной дочери Сони', long: 'Умерла от воспаления лёгких.', place: { label: 'Женева' }, chapter: 'europe', works: [{ ref: 'idiot', note: 'работа над романом шла в эти недели' }] },
  ],
  works: [
    { ref: 'igrok', title: 'Игрок', written: { from: 1866, to: 1866 }, work_id: 88 },
    { ref: 'idiot', title: 'Идиот', written: { from: 1867, to: 1869 }, work_id: 89 },
  ],
  context: [{ key: 'ru-1866', from: 1866, short: 'Покушение Каракозова на Александра II' }],
  sources: [{ id: 'ruwiki', url: 'https://ru.wikipedia.org/wiki/X' }],
  version: 'test',
};

const lifeContext = {
  available: true,
  author: { id: 17, name: 'Достоевский Фёдор Михайлович' },
  period: { from: 1867, to: 1869 },
  items: [
    { event: timeline.events[3], relation: { kind: 'during' }, linked: false },
    { event: timeline.events[4], relation: { kind: 'during' }, linked: true, note: 'роман целиком написан за границей' },
    { event: timeline.events[5], relation: { kind: 'during' }, linked: true, note: 'работа над романом шла в эти недели' },
  ],
};

const json = (body: unknown) => ({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });

test('карточка автора: тизер с полоской и вехами; без таймлайна — тизера нет', async ({ page }) => {
  await mockApi(page);
  await page.route(/\/api\/authors\/17\/bio-timeline$/, (route) => route.fulfill(json(timeline)));
  await page.goto('/authors/17');
  const teaser = page.getByTestId('bio-timeline-teaser');
  await expect(teaser).toBeVisible();
  await expect(teaser.getByTestId('life-strip').locator('svg')).toBeVisible();
  // Тизер — major с линками: Стелловский, Европа, Соня; minor «Время» — нет.
  await expect(teaser.getByText('Долги и кабальный договор со Стелловским')).toBeVisible();
  await expect(teaser.getByText('Журнал «Время» вместе с братом')).toHaveCount(0);
  await expect(teaser.getByRole('link', { name: /Открыть таймлайн · 6 вех/ })).toHaveAttribute('href', '/authors/17/timeline');
  // Наведение на строку подсвечивает её и притушает остальные.
  await teaser.getByText('Смерть трёхмесячной дочери Сони').hover();
  await expect(teaser.locator('li.opacity-40').first()).toBeVisible();

  await page.route(/\/api\/authors\/17\/bio-timeline$/, (route) => route.fulfill(json({ available: false })));
  await page.goto('/authors/17');
  await expect(page.getByText(authorDetailFixture.full_name).first()).toBeVisible();
  await expect(page.getByTestId('bio-timeline-teaser')).toHaveCount(0);
});

test('карточка книги: блок «В жизни автора в это время» с датами, пометками и ссылкой', async ({ page }) => {
  await mockApi(page);
  // У фикстуры книги нет work_id — окно считается по работе.
  await page.route(/\/api\/books\/19$/, (route) => route.fulfill(json({ ...bookDetailFixture, work_id: 89 })));
  await page.route(/\/api\/works\/\d+\/life-context$/, (route) => route.fulfill(json(lifeContext)));
  await page.goto(`/books/${bookDetailFixture.id}`);
  const block = page.getByTestId('life-context');
  await expect(block).toBeVisible();
  await expect(block.getByText('писалась в 1867–1869')).toBeVisible();
  await expect(block.getByText('24 мая 1868')).toBeVisible();
  await expect(block.getByText('во время работы над книгой').first()).toBeVisible();
  await expect(block.getByText('работа над романом шла в эти недели')).toBeVisible();
  await expect(block.getByRole('link', { name: 'Вся жизнь и книги →' })).toHaveAttribute('href', '/authors/17/timeline');
  // Клик по вехе раскрывает long.
  await block.getByRole('button', { name: 'Смерть трёхмесячной дочери Сони' }).click();
  await expect(block.getByText('Умерла от воспаления лёгких. · Женева')).toBeVisible();
});

test('полная страница: главы, тумблеры, ось; 375 px без горизонтального скролла', async ({ page }) => {
  await mockApi(page);
  await page.route(/\/api\/authors\/17\/bio-timeline$/, (route) => route.fulfill(json(timeline)));
  await page.goto('/authors/17/timeline');
  const root = page.getByTestId('author-timeline');
  await expect(root.getByRole('heading', { name: 'Жизнь и книги' })).toBeVisible();
  await expect(root.getByText('1821–1881 · 6 вех')).toBeVisible();
  await expect(root.getByTestId('timeline-chapter')).toHaveCount(2);
  // minor скрыты по умолчанию, тумблер показывает; фон — тоже тумблером.
  await expect(root.getByText('Журнал «Время» вместе с братом')).toHaveCount(0);
  await root.getByText('Второстепенные').click();
  await expect(root.getByText('Журнал «Время» вместе с братом')).toBeVisible();
  await expect(root.getByText('Покушение Каракозова')).toHaveCount(0);
  await root.getByText('Исторический фон').click();
  await expect(root.getByText('Покушение Каракозова на Александра II')).toBeVisible();
  // Чип книги у линкованной вехи ведёт на работу каталога.
  await expect(root.getByRole('link', { name: /Идиот 1867–1869/ }).first()).toHaveAttribute('href', '/works/89');
  // Книги периода у заголовка главы — из датасета, по одной на произведение и
  // ссылкой на работу (#529), а не издания каталожной статистики (year_stats).
  const shelf = (title: string) =>
    root.getByTestId('timeline-chapter').filter({ hasText: title }).getByTestId('chapter-books').getByRole('link');
  await expect(shelf('Возвращение, журналы, долги')).toHaveCount(1);
  await expect(shelf('Возвращение, журналы, долги')).toHaveAccessibleName(/Игрок 1866/);
  await expect(shelf('Возвращение, журналы, долги')).toHaveAttribute('href', '/works/88');
  await expect(shelf('Анна, Европа, рулетка')).toHaveCount(1);
  await expect(shelf('Анна, Европа, рулетка')).toHaveAttribute('href', '/works/89');
  // Ось.
  await root.getByRole('button', { name: 'Ось' }).click();
  await expect(root.getByTestId('timeline-axis')).toBeVisible();
  await expect(root.getByText('Смерть трёхмесячной дочери Сони')).toBeVisible();

  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('/authors/17/timeline');
  await expect(page.getByTestId('author-timeline')).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
  await page.getByRole('button', { name: 'Ось' }).click();
  await expect(page.getByTestId('timeline-axis')).toBeVisible();
  const overflowAxis = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflowAxis).toBeLessThanOrEqual(1);
});

test('ось: подписи вех слева — в одну строку и не налезают друг на друга (#519)', async ({ page }) => {
  // Как у Дика в 1960-х: вехи год за годом, у двух — по три линка с длинными названиями.
  const dense = {
    ...timeline,
    name: 'Филип Дик',
    born: '1928-12-16',
    died: '1982-03-02',
    chapters: [{ key: 'sixties', title: 'Шестидесятые', from: 1960, to: 1969 }],
    events: [
      { key: 'career-1963', date: { from: '1963-09', precision: 'month' }, type: 'career', tier: 'major', short: 'Премия «Хьюго» за роман об альтернативной истории', chapter: 'sixties', works: [{ ref: 'castle' }] },
      { key: 'creation-1964', date: { from: '1964', precision: 'year' }, type: 'creation', tier: 'major', short: 'Одиннадцать романов за два года на амфетаминах', chapter: 'sixties', works: [{ ref: 'palmer' }, { ref: 'martian' }, { ref: 'clans' }] },
      { key: 'faith-1964', date: { from: '1964-06', precision: 'month' }, type: 'faith', tier: 'major', short: 'Крещение в Епископальной церкви', long: 'Вместе с женой Анной.', chapter: 'sixties' },
      { key: 'family-1966', date: { from: '1966', precision: 'year' }, type: 'family', tier: 'major', short: 'Брак с Нэнси Хакетт', chapter: 'sixties', works: [{ ref: 'ubik' }, { ref: 'androids' }, { ref: 'tears' }] },
    ],
    works: [
      { ref: 'castle', title: 'Человек в высоком замке', written: { from: 1961, to: 1962 } },
      { ref: 'palmer', title: 'Три стигмата Палмера Элдрича', written: { from: 1964, to: 1964 } },
      { ref: 'martian', title: 'Сдвиг времени по-марсиански', written: { from: 1962, to: 1963 } },
      { ref: 'clans', title: 'Кланы Альфанской луны', written: { from: 1963, to: 1964 } },
      { ref: 'ubik', title: 'Убик', written: { from: 1966, to: 1966 } },
      { ref: 'androids', title: 'Мечтают ли андроиды об электроовцах?', written: { from: 1966, to: 1966 } },
      { ref: 'tears', title: 'Пролейтесь, слёзы, сказал полицейский', written: { from: 1970, to: 1970 } },
    ],
    context: [],
  };
  await mockApi(page);
  await page.route(/\/api\/authors\/17\/bio-timeline$/, (route) => route.fulfill(json(dense)));
  await page.goto('/authors/17/timeline');
  const root = page.getByTestId('author-timeline');
  await root.getByRole('button', { name: 'Ось' }).click();
  const labels = root.getByTestId('timeline-axis').getByTestId('axis-event');
  await expect(labels).toHaveCount(4);
  // Хвост линков — первое название и «+N», полный список — в title.
  const tail = labels.filter({ hasText: 'Брак с Нэнси Хакетт' }).locator('[title]');
  await expect(tail).toHaveText(/→ Убик\s\+2/);
  await expect(tail).toHaveAttribute('title', '→ Убик, Мечтают ли андроиды об электроовцах?, Пролейтесь, слёзы, сказал полицейский');

  const boxes = (await labels.evaluateAll((els) => els.map((el) => el.getBoundingClientRect().toJSON() as DOMRect))).sort(
    (a, b) => a.top - b.top,
  );
  for (const b of boxes) expect(b.height).toBeLessThanOrEqual(24); // одна строка
  for (let i = 1; i < boxes.length; i++) expect(boxes[i].top).toBeGreaterThanOrEqual(boxes[i - 1].bottom - 0.5);
  // Подпись не уходит за свою половину (на ось и правую дорожку работ).
  const axisBox = (await root.getByTestId('timeline-axis').boundingBox())!;
  for (const b of boxes) expect(b.right).toBeLessThanOrEqual(axisBox.x + axisBox.width / 2);
});

test('админ в режиме правки скрывает веху и возвращает её', async ({ page }) => {
  await mockApi(page);
  let hidden = false;
  await page.route(/\/api\/authors\/17\/bio-timeline$/, (route) =>
    route.fulfill(json({ ...timeline, events: timeline.events.map((e) => (e.key === 'loss-1868' ? { ...e, hidden } : e)) })),
  );
  const patched: string[] = [];
  await page.route(/\/api\/admin\/bio-timeline\/Q991\/events\/loss-1868$/, async (route) => {
    const body = route.request().postDataJSON() as { hidden: boolean };
    hidden = body.hidden;
    patched.push(String(body.hidden));
    await route.fulfill(json({ qid: 'Q991', key: 'loss-1868', hidden }));
  });
  await page.goto('/authors/17/timeline');
  const root = page.getByTestId('author-timeline');
  // Вне режима правки кнопки нет.
  await expect(root.getByRole('button', { name: 'Скрыть веху' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Править' }).click();
  const row = root.getByTestId('timeline-event').filter({ hasText: 'Смерть трёхмесячной дочери Сони' });
  await row.hover();
  await row.getByRole('button', { name: 'Скрыть веху' }).click();
  await expect(row.getByRole('button', { name: 'Вернуть веху' })).toBeVisible();
  await row.getByRole('button', { name: 'Вернуть веху' }).click();
  await expect(row.getByRole('button', { name: 'Скрыть веху' })).toBeAttached();
  expect(patched).toEqual(['true', 'false']);
});
