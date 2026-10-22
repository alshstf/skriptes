import { test, expect } from './_fixtures';

// Лента Главной (план recsys/01, 1.3): у карточек есть причина «почему это
// здесь», причина не вылезает за ширину карточки, скрытие — с причиной через
// kebab, и карточка исчезает из секции.

type Item = {
  id: number;
  work_id: number;
  title: string;
  authors: string[];
  lib_id: string;
  series?: string;
  reasons: Record<string, unknown>[];
};

const CONTINUE: Item = {
  id: 11,
  work_id: 1011,
  title: 'Недочитанная книга',
  authors: ['Автор Первый'],
  lib_id: 'L11',
  reasons: [
    { kind: 'in_progress', fraction: 0.42, at: new Date(Date.now() - 3 * 86_400_000).toISOString(), minutes: 84 },
  ],
};

const FEED: Item = {
  id: 12,
  work_id: 1012,
  title: 'Очень длинное название новинки подписанного автора, которое должно обрезаться',
  authors: ['Автор Подписанный'],
  lib_id: 'L12',
  series: 'Мой цикл',
  reasons: [
    { kind: 'new_by_author', author_id: 5, author: 'Автор Подписанный Имя Отчество', at: '2026-10-19' },
  ],
};

const APPEARED: Item = {
  id: 14,
  work_id: 1014,
  title: 'Найденная книга',
  authors: ['Автор Искомый'],
  lib_id: 'L14',
  reasons: [{ kind: 'searched_appeared', query: 'найденная книга', at: '2026-10-10T00:00:00Z' }],
};

const NEXT: Item = {
  id: 21,
  work_id: 1021,
  title: 'Том третий',
  authors: ['Автор Цикла'],
  lib_id: 'L21',
  series: 'Мой цикл',
  reasons: [
    {
      kind: 'next_in_series',
      series_id: 9,
      series: 'Мой цикл',
      work_id: 1020,
      title: 'Том второй',
      ser_no: 3,
      at: '2026-10-15T00:00:00Z',
    },
  ],
};

const BY_AUTHOR: Item = {
  id: 22,
  work_id: 1022,
  title: 'Ещё роман',
  authors: ['Любимый Автор'],
  lib_id: 'L22',
  reasons: [{ kind: 'author_you_finished', author_id: 5, author: 'Любимый Автор', work_id: 1023, title: 'Первый роман' }],
};

type HomeMock = {
  // Секции поверх дефолтов (continue_reading → CONTINUE, searched_appeared → APPEARED,
  // subscriptions → feed.ref, остальные пустые).
  sections?: Partial<Record<string, Item[]>>;
  // Серверные настройки ленты: GET отдаёт state, PUT обновляет его и пишет тело в puts.
  feed?: { collapsed: string[] };
  puts?: unknown[];
};

function mockHome(page: import('@playwright/test').Page, feed: { ref: Item[] }, opts: HomeMock = {}) {
  page.route(/\/api\/me\/recommendations\?/, (route) => {
    const section = new URL(route.request().url()).searchParams.get('section') ?? '';
    const defaults: Record<string, Item[]> = {
      continue_reading: [CONTINUE],
      searched_appeared: [APPEARED],
      subscriptions: feed.ref,
    };
    const items = opts.sections?.[section] ?? defaults[section] ?? [];
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ section, items }),
    });
  });
  const state = opts.feed ?? { collapsed: [] };
  page.route(/\/api\/me\/home-feed$/, (route) => {
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON() as { collapsed: string[] };
      opts.puts?.push(body);
      state.collapsed = body.collapsed;
    }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(state) });
  });
}

test('home: причины под карточками и ссылка на автора', async ({ mockedPage: page }) => {
  mockHome(page, { ref: [FEED] });
  await page.goto('/');

  await expect(page.getByRole('heading', { name: 'Продолжить чтение' })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(/Остановились на 42\s% · 3 дня назад · ещё ≈ 1 ч 20 мин/)).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Появилось то, что вы искали' })).toBeVisible();
  await expect(page.getByText('Вы искали «найденная книга»', { exact: false })).toBeVisible();

  const feedReason = page.getByTestId('rec-reason').filter({ hasText: 'По подписке на автора' });
  await expect(feedReason).toBeVisible();
  const link = feedReason.getByRole('link', { name: 'Автор Подписанный Имя Отчество' });
  await expect(link).toHaveAttribute('href', '/authors/5');

  // Причина не шире карточки (line-clamp, без горизонтального переполнения).
  const card = page.getByRole('link', { name: /Очень длинное название новинки/ });
  const cardBox = await card.boundingBox();
  const reasonBox = await feedReason.boundingBox();
  expect(cardBox && reasonBox).toBeTruthy();
  expect(reasonBox!.width).toBeLessThanOrEqual(cardBox!.width + 1);
  expect(reasonBox!.height).toBeLessThanOrEqual(3 * 16 + 2); // не больше двух строк xs

  // Клик по ссылке причины ведёт на автора, не на книгу.
  await link.click();
  await expect(page).toHaveURL(/\/authors\/5$/);
});

test('home: «Уже читал» скрывает новинку с причиной', async ({ mockedPage: page }) => {
  const feed = { ref: [FEED] };
  mockHome(page, feed);
  let body: unknown;
  await page.route(/\/api\/me\/feed\/dismiss$/, (route) => {
    body = route.request().postDataJSON();
    feed.ref = [];
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ dismissed: true }) });
  });

  await page.goto('/');
  await expect(page.getByText(/Очень длинное название новинки/)).toBeVisible({ timeout: 10_000 });

  // Kebab есть и у «Появилось то, что вы искали» — берём из секции новинок.
  const feedSection = page.locator('section', { has: page.getByRole('heading', { name: 'Новинки по подпискам' }) });
  await feedSection.getByRole('button', { name: 'Скрыть из ленты' }).click();
  await page.getByRole('menuitem', { name: 'Уже читал' }).click();

  await expect(page.getByText(/Очень длинное название новинки/)).toHaveCount(0);
  expect(body).toEqual({ work_id: 1012, reason: 'already_read' });
});

test('home: на телефоне лента без горизонтального переполнения', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  mockHome(page, { ref: [FEED, { ...FEED, id: 13, work_id: 1013, title: 'Вторая новинка' }] });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Новинки по подпискам' })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('rec-reason').first()).toBeVisible();
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  if (process.env.HOME_RECS_SHOTS) {
    await page.screenshot({ path: `${process.env.HOME_RECS_SHOTS}/home-mobile.png`, fullPage: true });
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.screenshot({ path: `${process.env.HOME_RECS_SHOTS}/home-desktop.png`, fullPage: true });
  }
});

// Правила фазы 2 (план recsys/02, 2.1/2.2/2.9): порядок секций, причины с
// основанием и ссылками на цикл/автора.
test('home: секции правил — порядок, причины, ссылки', async ({ mockedPage: page }) => {
  mockHome(page, { ref: [FEED] }, { sections: { next_in_series: [NEXT], author_you_finished: [BY_AUTHOR] } });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Дальше в цикле' })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('heading', { name: 'Автор, которого вы дочитали' })).toBeVisible();

  const titles = [
    'Продолжить чтение',
    'Появилось то, что вы искали',
    'Дальше в цикле',
    'Автор, которого вы дочитали',
    'Новинки по подпискам',
    'Оцените прочитанное',
  ];
  const headings = (await page.getByRole('heading', { level: 2 }).allTextContents()).filter((h) =>
    titles.includes(h),
  );
  expect(headings).toEqual(titles.slice(0, 5)); // «Оцените прочитанное» пуста — не рендерится

  const next = page.getByTestId('rec-reason').filter({ hasText: 'Дочитали «Том второй» · № 3 в цикле' });
  await expect(next).toBeVisible();
  await expect(next.getByRole('link', { name: 'Мой цикл' })).toHaveAttribute('href', '/series/9');
  const author = page.getByTestId('rec-reason').filter({ hasText: 'Дочитали «Первый роман» · автор:' });
  await expect(author).toBeVisible();
  await expect(author.getByRole('link', { name: 'Любимый Автор' })).toHaveAttribute('href', '/authors/5');
  if (process.env.HOME_RECS_SHOTS) {
    await page.setViewportSize({ width: 1280, height: 1100 });
    await page.screenshot({ path: `${process.env.HOME_RECS_SHOTS}/home-rules-desktop.png`, fullPage: true });
    await page.setViewportSize({ width: 375, height: 812 });
    await page.screenshot({ path: `${process.env.HOME_RECS_SHOTS}/home-rules-mobile.png`, fullPage: true });
  }
});

test('home: сворачивание секции сохраняется на сервере', async ({ mockedPage: page }) => {
  const puts: unknown[] = [];
  mockHome(page, { ref: [FEED] }, { sections: { next_in_series: [NEXT] }, feed: { collapsed: [] }, puts });
  await page.goto('/');
  await expect(page.getByText('Том третий')).toBeVisible({ timeout: 10_000 });

  await page.getByRole('button', { name: 'Свернуть «Дальше в цикле»' }).click();
  await expect(page.getByText('Том третий')).toHaveCount(0);
  const section = page.locator('section', { has: page.getByRole('heading', { name: 'Дальше в цикле' }) });
  await expect(section.getByText(/^1 книга$/)).toBeVisible();
  await expect.poll(() => puts).toEqual([{ collapsed: ['next_in_series'] }]);
  // Остальные секции не тронуты.
  await expect(page.getByText(/Очень длинное название новинки/)).toBeVisible();

  // После перезагрузки — свёрнута по данным с сервера; развернуть — PUT с пустым списком.
  await page.reload();
  const expand = page.getByRole('button', { name: 'Развернуть «Дальше в цикле»' });
  await expect(expand).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText('Том третий')).toHaveCount(0);
  await expand.click();
  await expect(page.getByText('Том третий')).toBeVisible();
  await expect.poll(() => puts.at(-1)).toEqual({ collapsed: [] });
});

test('home: пустая лента — подсказка новому пользователю', async ({ mockedPage: page }) => {
  mockHome(page, { ref: [] }, { sections: { continue_reading: [], searched_appeared: [] } });
  await page.goto('/');
  await expect(page.getByTestId('empty-feed')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText('Пока нечего рекомендовать', { exact: false })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Новинки по подпискам' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Открыть каталог' })).toHaveAttribute('href', '/books');
});

// Второй минор фазы 2 (2.3–2.5): секции «Отложенное, к которому доросли»,
// «Забытая классика», «Перечитать» — после подписок, перед «Оцените прочитанное».
test('home: секции брошенного, классики и перечитывания', async ({ mockedPage: page }) => {
  const DROPPED: Item = {
    id: 31,
    work_id: 1031,
    title: 'Брошенная книга',
    authors: ['Автор Полюбленный'],
    lib_id: 'L31',
    reasons: [
      {
        kind: 'dropped_then_loved_author',
        author_id: 8,
        author: 'Автор Полюбленный',
        count: 2,
        at: new Date(Date.now() - 200 * 86_400_000).toISOString(),
      },
    ],
  };
  const CLASSIC: Item = {
    id: 32,
    work_id: 1032,
    title: 'Двенадцать стульев',
    authors: ['Ильф Илья'],
    lib_id: 'L32',
    reasons: [{ kind: 'forgotten_classic', year: 1927, rating: 9.29, source: 'fantlab' }],
  };
  const REREAD: Item = {
    id: 33,
    work_id: 1033,
    title: 'Любимая книга',
    authors: ['Автор Любимый'],
    lib_id: 'L33',
    reasons: [{ kind: 'reread', at: new Date(Date.now() - 4 * 365 * 86_400_000).toISOString() }],
  };
  mockHome(
    page,
    { ref: [FEED] },
    { sections: { dropped_then_loved_author: [DROPPED], forgotten_classic: [CLASSIC], reread: [REREAD] } },
  );
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Забытая классика' })).toBeVisible({ timeout: 10_000 });
  const titles = [
    'Новинки по подпискам',
    'Отложенное, к которому доросли',
    'Забытая классика',
    'Перечитать',
    'Оцените прочитанное',
  ];
  const headings = (await page.getByRole('heading', { level: 2 }).allTextContents()).filter((h) =>
    titles.includes(h),
  );
  expect(headings).toEqual(titles.slice(0, 4));
  const dropped = page.getByTestId('rec-reason').filter({ hasText: 'с тех пор 2 книги автора:' });
  await expect(dropped).toBeVisible();
  await expect(dropped.getByRole('link', { name: 'Автор Полюбленный' })).toHaveAttribute('href', '/authors/8');
  await expect(page.getByText('1927 год · Фантлаб 9.3 · здесь ещё никто не читал')).toBeVisible();
  await expect(page.getByText(/Поставили 5 · читали 4 года назад · перечитать\?/)).toBeVisible();
});

// «На вечер» (2.6): чипы 20/40/60 в заголовке, выбор уходит в запрос (&minutes=)
// и запоминается в localStorage; пустая секция остаётся с подсказкой.
test('home: «На вечер» — чипы минут и пустое состояние', async ({ mockedPage: page }) => {
  const TONIGHT: Item = {
    id: 41,
    work_id: 1041,
    title: 'Короткая повесть',
    authors: ['Автор Короткий'],
    lib_id: 'L41',
    reasons: [{ kind: 'fits_tonight', minutes: 32, genre: 'sf_action' }],
  };
  mockHome(page, { ref: [FEED] });
  // Роуты Playwright матчатся с последнего зарегистрированного — свой после mockHome.
  const requested: string[] = [];
  page.route(/\/api\/me\/recommendations\?section=fits_tonight/, (route) => {
    const u = new URL(route.request().url());
    const minutes = u.searchParams.get('minutes') ?? '';
    requested.push(minutes);
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ section: 'fits_tonight', items: minutes === '60' ? [TONIGHT] : [] }),
    });
  });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'На вечер' })).toBeVisible({ timeout: 10_000 });
  // По умолчанию 40 минут, пусто — подсказка вместо карточек, чипы на месте.
  await expect.poll(() => requested).toContain('40');
  await expect(page.getByTestId('tonight-empty')).toContainText('За 40 минут');
  const chip60 = page.getByRole('button', { name: '60 мин' });
  await expect(page.getByRole('button', { name: '40 мин' })).toHaveAttribute('aria-pressed', 'true');

  await chip60.click();
  await expect(chip60).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByText('Короткая повесть')).toBeVisible();
  // Имя жанра из /api/genres (fixture: sf_action → «Боевая фантастика»).
  await expect(page.getByText('≈ 30 мин · ваш жанр: Боевая фантастика')).toBeVisible();
  expect(requested.at(-1)).toBe('60');

  // Выбор пережил перезагрузку.
  await page.reload();
  await expect(page.getByRole('button', { name: '60 мин' })).toHaveAttribute('aria-pressed', 'true', {
    timeout: 10_000,
  });
  await expect(page.getByText('Короткая повесть')).toBeVisible();
  if (process.env.HOME_RECS_SHOTS) {
    await page.getByRole('heading', { name: 'На вечер' }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${process.env.HOME_RECS_SHOTS}/home-tonight.png` });
    await page.setViewportSize({ width: 375, height: 812 });
    await page.getByRole('heading', { name: 'На вечер' }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${process.env.HOME_RECS_SHOTS}/home-tonight-mobile.png` });
  }
});

// Фаза 3 (recsys/03): секция «Вы хотели прочитать» с причиной; в kebab карточки
// правила — «Не предлагать автора / цикл» (author_ids, series_id) → POST …/block.
const TO_READ = {
  id: 31,
  work_id: 1031,
  title: 'Отложенная книга',
  authors: ['Автор Отложенный'],
  author_ids: [7],
  lib_id: 'L31',
  series: 'Цикл отложенный',
  series_id: 12,
  reasons: [{ kind: 'to_read', at: new Date(Date.now() - 70 * 86_400_000).toISOString() }],
} as Item;

test('home: «Вы хотели прочитать» и «не предлагать автора» из kebab', async ({ mockedPage: page }) => {
  mockHome(page, { ref: [] }, { sections: { to_read_reminder: [TO_READ] } });
  const calls: string[] = [];
  await page.route(/\/api\/(authors|series)\/\d+\/block$/, (route) => {
    calls.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`);
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ is_blocked: true }) });
  });
  await page.goto('/');
  const section = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Вы хотели прочитать' }) });
  await expect(section).toBeVisible({ timeout: 10_000 });
  await expect(section.getByTestId('rec-reason')).toContainText('Хотели прочитать · отложили 2 месяца назад');
  await section.getByRole('button', { name: 'Скрыть из ленты' }).click();
  await expect(page.getByRole('menuitem', { name: 'Не предлагать цикл: Цикл отложенный' })).toBeVisible();
  await page.getByRole('menuitem', { name: 'Не предлагать автора: Автор Отложенный' }).click();
  await expect.poll(() => calls).toEqual(['POST /api/authors/7/block']);
});

// Настроение сессии (recsys/03, 3.4): чипы над лентой; выбор уходит в &mood=
// только секциям правил и «На вечер», меняет первые карточки (поднятая несёт
// «под настроение»), логируется POST /me/mood, живёт в localStorage;
// повторный клик снимает.
test('home: настроение — чип меняет первые карточки и логируется', async ({ mockedPage: page }) => {
  const FUNNY: Item = {
    id: 23,
    work_id: 1023,
    title: 'Смешной роман',
    authors: ['Любимый Автор'],
    lib_id: 'L23',
    reasons: [
      { kind: 'author_you_finished', author_id: 5, author: 'Любимый Автор', work_id: 1024, title: 'Первый роман', mood: 'funny' },
    ],
  };
  mockHome(page, { ref: [FEED] });
  const requested: Record<string, string[]> = {};
  page.route(/\/api\/me\/recommendations\?/, (route) => {
    const u = new URL(route.request().url());
    const section = u.searchParams.get('section') ?? '';
    const mood = u.searchParams.get('mood') ?? '';
    (requested[section] ??= []).push(mood);
    let items: Item[] = [];
    if (section === 'author_you_finished') items = mood === 'funny' ? [FUNNY, BY_AUTHOR] : [BY_AUTHOR];
    else if (section === 'continue_reading') items = [CONTINUE];
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ section, items }) });
  });
  const logged: unknown[] = [];
  await page.route(/\/api\/me\/mood$/, (route) => {
    logged.push(route.request().postDataJSON());
    void route.fulfill({ status: 200, contentType: 'application/json', body: '{"mood":"funny"}' });
  });

  await page.goto('/');
  const section = page.locator('section', { has: page.getByRole('heading', { name: 'Автор, которого вы дочитали' }) });
  await expect(section).toBeVisible({ timeout: 10_000 });
  const cards = () => section.locator('a[href^="/works/"]').evaluateAll((els) => els.map((e) => e.getAttribute('href')));
  await expect.poll(cards).toEqual(['/works/1022']);
  const chips = page.getByRole('group', { name: 'Настроение' });
  const funny = chips.getByRole('button', { name: 'смешное' });
  await expect(funny).toHaveAttribute('aria-pressed', 'false');

  await funny.click();
  await expect(funny).toHaveAttribute('aria-pressed', 'true');
  await expect.poll(cards).toEqual(['/works/1023', '/works/1022']);
  await expect(section.getByTestId('rec-reason').first()).toContainText('под настроение «смешное»');
  await expect.poll(() => logged).toEqual([{ mood: 'funny' }]);
  expect(requested.author_you_finished).toContain('funny');
  expect(requested.continue_reading).not.toContain('funny');

  // Пережило перезагрузку (localStorage), второй лог не пишется.
  await page.reload();
  await expect(page.getByRole('group', { name: 'Настроение' }).getByRole('button', { name: 'смешное' })).toHaveAttribute(
    'aria-pressed',
    'true',
    { timeout: 10_000 },
  );
  await expect.poll(cards).toEqual(['/works/1023', '/works/1022']);
  expect(logged).toHaveLength(1);

  // Повторный клик снимает настроение — порядок прежний.
  await page.getByRole('group', { name: 'Настроение' }).getByRole('button', { name: 'смешное' }).click();
  await expect(page.getByRole('group', { name: 'Настроение' }).getByRole('button', { name: 'смешное' })).toHaveAttribute(
    'aria-pressed',
    'false',
  );
  await expect.poll(cards).toEqual(['/works/1022']);
  if (process.env.HOME_RECS_SHOTS) {
    await page.getByRole('group', { name: 'Настроение' }).getByRole('button', { name: 'смешное' }).click();
    await page.screenshot({ path: `${process.env.HOME_RECS_SHOTS}/home-mood.png` });
    await page.setViewportSize({ width: 375, height: 812 });
    await page.screenshot({ path: `${process.env.HOME_RECS_SHOTS}/home-mood-mobile.png` });
  }
});
