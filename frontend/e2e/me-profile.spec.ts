import { test, expect } from './_fixtures';

/**
 * /me — секция «Профиль»: смена display_name + пароля. Kindle-target'ы
 * проверяются в отдельном spec'е (send-to-kindle.spec.ts).
 *
 * Сценарии:
 *  1. Inline-edit display_name → PATCH /api/me с правильным body + toast.
 *  2. Смена пароля: форма раскрывается по кнопке, отправка PATCH
 *     /api/me/password.
 *  3. Кнопка submit пароля disabled пока поля невалидны (mismatch / too short).
 */

test('me: inline-edit display_name → PATCH /api/me', async ({ mockedPage: page }) => {
  let patchBody: unknown = null;
  await page.route(/\/api\/me$/, (route) => {
    if (route.request().method() === 'PATCH') {
      patchBody = JSON.parse(route.request().postData() ?? '{}');
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          user: {
            id: 1,
            email: 'tester@example.com',
            display_name: 'Updated Name',
            role: 'admin',
            created_at: '2026-05-10T00:00:00Z',
          },
        }),
      });
      return;
    }
    route.continue();
  });
  // /api/me/kindle-targets — пустой список чтобы не падали другие части UI.
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '{"items":[]}',
    }),
  );

  await page.goto('/me');

  // Секция «Профиль» с двумя editable-полями.
  await expect(page.getByRole('heading', { name: /Профиль/ })).toBeVisible({
    timeout: 10_000,
  });

  // Клик pencil рядом с именем → раскрывается inline form, label='Имя'.
  await page.getByRole('button', { name: 'Изменить name' }).click();
  const input = page.getByLabel('Имя');
  await input.fill('Updated Name');
  await page.getByRole('button', { name: 'Сохранить' }).click();

  await expect.poll(() => patchBody).toEqual({ display_name: 'Updated Name' });
  await expect(page.getByText('Имя обновлено')).toBeVisible({ timeout: 5_000 });
});

test('me: inline-edit email → PATCH /api/me с email', async ({ mockedPage: page }) => {
  let patchBody: unknown = null;
  await page.route(/\/api\/me$/, (route) => {
    if (route.request().method() === 'PATCH') {
      patchBody = JSON.parse(route.request().postData() ?? '{}');
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          user: {
            id: 1,
            email: 'updated@example.com',
            display_name: 'Tester',
            role: 'admin',
            created_at: '2026-05-10T00:00:00Z',
          },
        }),
      });
      return;
    }
    route.continue();
  });
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '{"items":[]}',
    }),
  );

  await page.goto('/me');
  await expect(page.getByRole('heading', { name: /Профиль/ })).toBeVisible({
    timeout: 10_000,
  });

  // Email редактируется отдельной кнопкой. Scope по id чтобы не зацепить
  // одноимённое поле «Email» в Kindle-targets form ниже.
  await page.getByRole('button', { name: 'Изменить email' }).click();
  const input = page.locator('#field-email');
  await input.fill('updated@example.com');
  // helpText «email = логин» должен быть виден в edit-режиме.
  await expect(page.getByText(/Email используется для входа/)).toBeVisible();

  await page.getByRole('button', { name: 'Сохранить' }).click();

  await expect.poll(() => patchBody).toEqual({ email: 'updated@example.com' });
  await expect(page.getByText('Email обновлён')).toBeVisible({ timeout: 5_000 });
});

test('me: email без @ → validation error, submit disabled', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '{"items":[]}',
    }),
  );

  await page.goto('/me');
  await page.getByRole('button', { name: 'Изменить email' }).click();
  const input = page.locator('#field-email');
  await input.fill('not-an-email');

  await expect(page.getByText('Email должен содержать «@».')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Сохранить' })).toBeDisabled();
});

test('me: смена пароля — submit disabled пока поля невалидны', async ({
  mockedPage: page,
}) => {
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );

  await page.goto('/me');
  await page.getByRole('button', { name: 'Сменить пароль' }).click();
  await expect(page.getByLabel('Текущий пароль')).toBeVisible();

  const submit = page.getByRole('button', { name: 'Обновить' });
  await expect(submit).toBeDisabled();

  await page.getByLabel('Текущий пароль').fill('current-pass');
  await page.getByLabel('Новый пароль (мин. 8 символов)').fill('short'); // < 8
  await page.getByLabel('Повторите новый пароль').fill('short');
  await expect(submit).toBeDisabled();
  await expect(page.getByText('Минимум 8 символов.')).toBeVisible();

  await page.getByLabel('Новый пароль (мин. 8 символов)').fill('long-enough-1');
  await page.getByLabel('Повторите новый пароль').fill('mismatched');
  await expect(submit).toBeDisabled();
  await expect(page.getByText('Пароли не совпадают.')).toBeVisible();

  await page.getByLabel('Повторите новый пароль').fill('long-enough-1');
  await expect(submit).toBeEnabled();
});

test('me: смена пароля — успешный submit, toast', async ({ mockedPage: page }) => {
  let patchCalls = 0;
  await page.route(/\/api\/me\/password$/, (route) => {
    patchCalls++;
    route.fulfill({ status: 204, body: '' });
  });
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );

  await page.goto('/me');
  await page.getByRole('button', { name: 'Сменить пароль' }).click();
  await page.getByLabel('Текущий пароль').fill('old-password-1');
  await page.getByLabel('Новый пароль (мин. 8 символов)').fill('new-password-1');
  await page.getByLabel('Повторите новый пароль').fill('new-password-1');
  await page.getByRole('button', { name: 'Обновить' }).click();

  await expect.poll(() => patchCalls).toBe(1);
  await expect(page.getByText('Пароль обновлён')).toBeVisible({ timeout: 5_000 });
});
