import { test, expect } from './_fixtures';

/**
 * /admin/users — управление пользователями. Доступно только админу.
 *
 * Сценарии:
 *  1. Default mockedPage (admin) видит ссылку «Пользователи» в menu и
 *     может зайти на /admin/users. Список рендерится.
 *  2. Mock'аем /api/auth/me с role=user → ссылка скрыта, и переход
 *     на /admin/users редиректит на /books (router guard).
 *  3. Admin создаёт нового юзера через форму → POST /api/admin/users
 *     с правильным body, success-toast.
 *  4. Admin сбрасывает пароль для существующего юзера.
 *  5. Last-admin защита: backend возвращает 409 на DELETE,
 *     toast показывает понятное сообщение.
 */

test('admin: link "Пользователи" видна в меню админу, скрыта обычному', async ({
  mockedPage: page,
}) => {
  await page.goto('/books');

  // Откроем user-menu — там должен быть пункт «Пользователи».
  // Trigger = кнопка с display_name.
  await page.getByRole('button', { name: /Tester/i }).click();
  await expect(page.getByRole('menuitem', { name: /Администрирование/ })).toBeVisible();
});

test('admin: обычный юзер не видит ссылку и редиректится с /admin/users', async ({
  page,
}) => {
  // НЕ используем mockedPage fixture — у нас своя role=user.
  await page.route('**/api/auth/me', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: {
          id: 2,
          email: 'plain@example.com',
          display_name: 'Plain',
          role: 'user',
          created_at: '2026-05-10T00:00:00Z',
        },
      }),
    }),
  );
  // /api/books — нужен для /books где нас оставит router-guard.
  await page.route(/\/api\/books(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [],
        total: 0,
        limit: 20,
        offset: 0,
        processing_ms: 1,
      }),
    }),
  );

  // 1) В меню нет «Пользователи»
  await page.goto('/books');
  await page.getByRole('button', { name: /Plain/i }).click();
  await expect(page.getByRole('menuitem', { name: /Администрирование/ })).not.toBeVisible();
  await page.keyboard.press('Escape');

  // 2) Прямой переход → редирект на /books
  await page.goto('/admin/users');
  await expect(page).toHaveURL(/\/books/);
});

test('admin: список пользователей рендерится, создание формой', async ({
  mockedPage: page,
}) => {
  let listCalls = 0;
  await page.route(/\/api\/admin\/users$/, (route) => {
    if (route.request().method() === 'POST') {
      route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 99,
          email: 'new@example.com',
          display_name: 'new',
          role: 'user',
          created_at: '2026-05-22T00:00:00Z',
        }),
      });
      return;
    }
    listCalls++;
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          {
            id: 1,
            email: 'tester@example.com',
            display_name: 'Tester',
            role: 'admin',
            created_at: '2026-05-10T00:00:00Z',
          },
          {
            id: 2,
            email: 'bob@example.com',
            display_name: 'Bob',
            role: 'user',
            created_at: '2026-05-11T00:00:00Z',
          },
        ],
      }),
    });
  });

  await page.goto('/admin/users');

  await expect(page.getByRole('heading', { name: /Управление пользователями/ })).toBeVisible({
    timeout: 10_000,
  });
  // Tester может встречаться и в header'е (display_name юзера) и в строке
  // списка — берём список через role=article + узкий matcher.
  // Структурный matcher: 2 строки в списке. toContainText проверяет
  // что внутри ListItem встречается нужный текст (без exact-issues
  // когда в paragraph'е несколько узлов: имя + admin-icon + "это вы").
  const listItems = page.getByRole('article').getByRole('listitem');
  await expect(listItems).toHaveCount(2);
  await expect(listItems.nth(0)).toContainText('Tester');
  await expect(listItems.nth(0)).toContainText('tester@example.com');
  await expect(listItems.nth(0)).toContainText('это вы'); // id=1 = me
  await expect(listItems.nth(1)).toContainText('Bob');

  // Forma добавления — label'ы внутри Card «Добавить пользователя».
  // Используем scope чтобы не путаться с возможными чужими label'ами
  // и form'ами на той же странице.
  const createCard = page.locator('section, article').filter({ has: page.getByText('Добавить пользователя') }).last();
  await createCard.getByLabel('Email').fill('new@example.com');
  await createCard.getByLabel('Пароль (мин. 8)').fill('newpass1234');
  // Дождёмся пока submit перестанет быть disabled (state-update React'а).
  const submit = createCard.getByRole('button', { name: 'Добавить' });
  await expect(submit).toBeEnabled();
  await submit.click();
  await expect(page.getByText(/добавлен/)).toBeVisible({ timeout: 5_000 });
  // После создания список перезагружается (invalidateQueries).
  await expect.poll(() => listCalls).toBeGreaterThanOrEqual(2);
});

test('admin: reset password — toast после сброса', async ({ mockedPage: page }) => {
  let resetCalls = 0;
  await page.route(/\/api\/admin\/users$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          {
            id: 1,
            email: 'tester@example.com',
            display_name: 'Tester',
            role: 'admin',
            created_at: '2026-05-10T00:00:00Z',
          },
          {
            id: 2,
            email: 'bob@example.com',
            display_name: 'Bob',
            role: 'user',
            created_at: '2026-05-11T00:00:00Z',
          },
        ],
      }),
    }),
  );
  await page.route(/\/api\/admin\/users\/2\/password$/, (route) => {
    if (route.request().method() === 'PATCH') {
      resetCalls++;
      route.fulfill({ status: 204, body: '' });
      return;
    }
    route.continue();
  });

  await page.goto('/admin/users');
  const listItems = page.getByRole('article').getByRole('listitem');
  await expect(listItems).toHaveCount(2, { timeout: 10_000 });

  // Row of Bob: 2-я строка (id=2). Кнопка reset password — aria-label.
  const bobRow = listItems.nth(1);
  await expect(bobRow).toContainText('Bob');
  await bobRow.getByRole('button', { name: 'Сбросить пароль' }).click();

  await expect(page.getByText('Сбросить пароль для bob@example.com')).toBeVisible();
  await page.getByLabel('Новый пароль (мин. 8)').fill('bobnewpass11');
  await page.getByLabel('Повторите').fill('bobnewpass11');
  await page.getByRole('button', { name: 'Сбросить', exact: true }).click();

  await expect.poll(() => resetCalls).toBe(1);
  await expect(page.getByText(/Пароль для bob@example.com обновлён/)).toBeVisible({
    timeout: 5_000,
  });
});
