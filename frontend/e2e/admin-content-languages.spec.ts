import { test, expect } from './_fixtures';

/**
 * /admin/content — режим «Новые языки: скрывать» (#310). Переключение
 * сохраняет то, что видно сейчас: показываемые = языки без креста, а
 * язык, впервые пришедший с будущим INPX, будет скрыт сам.
 */
test('admin content: режим «новые языки скрывать» сохраняет видимые языки', async ({
  mockedPage: page,
}) => {
  await page.route(/\/api\/languages(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          { code: 'ru', display: 'Русский', book_count: 10 },
          { code: 'en', display: 'Английский', book_count: 5 },
          { code: 'de', display: 'Немецкий', book_count: 2 },
        ],
      }),
    }),
  );
  let saved: Record<string, unknown> | null = null;
  await page.route(/\/api\/admin\/content$/, async (route) => {
    if (route.request().method() === 'PUT') {
      saved = route.request().postDataJSON();
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(saved) });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ hidden_genres: [], hidden_languages: ['en'], language_mode: '' }),
    });
  });

  await page.goto('/admin/content');
  await expect(page.getByRole('checkbox', { name: 'Скрыть язык: Английский' })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await page.getByRole('group', { name: 'Новые языки' }).getByRole('button', { name: 'Скрывать' }).click();
  await expect(page.getByText(/впервые появится с будущим INPX, будет скрыт сам/)).toBeVisible();
  // Видимое не изменилось: английский по-прежнему скрыт.
  await expect(page.getByRole('checkbox', { name: 'Скрыть язык: Английский' })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  // Скрываем ещё немецкий — в режиме «скрывать» это убирает его из показываемых.
  await page.getByRole('checkbox', { name: 'Скрыть язык: Немецкий' }).click();
  await page.getByRole('button', { name: 'Сохранить' }).click();

  await expect.poll(() => saved).not.toBeNull();
  expect(saved).toMatchObject({
    language_mode: 'only',
    shown_languages: ['ru'],
    hidden_languages: [],
  });
});
