import { test, expect } from './_fixtures';
import { authorDetailFixture } from './_fixtures';

test('author page: bio skeleton swaps to text after polling, photo renders', async ({
  page,
}) => {
  await page.route('**/api/auth/me', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: {
          id: 1,
          email: 'tester@example.com',
          display_name: 'Tester',
          role: 'admin',
          created_at: '2026-05-10T00:00:00Z',
        },
      }),
    }),
  );

  // Динамический mock: первый ответ без bio/photo, второй с обоими.
  let hits = 0;
  await page.route(/\/api\/authors\/17$/, (route) => {
    hits++;
    const body =
      hits === 1
        ? { ...authorDetailFixture, bio: undefined, photo_path: undefined }
        : {
            ...authorDetailFixture,
            bio: 'Краткая биография автора из Wikipedia.',
            photo_path: 'author-photo.jpg',
          };
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    });
  });
  await page.route(/\/api\/covers\//, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'image/png',
      body: Buffer.from(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=',
        'base64',
      ),
    }),
  );

  await page.goto('/authors/17');
  // Сначала видим заголовок "Биография" + скелетон.
  await expect(page.getByText('Биография', { exact: true })).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('[aria-label="Биография загружается"]')).toBeVisible();

  // Плейсхолдер фото (role=img, aria-label содержит "загружается").
  await expect(
    page.getByRole('img', { name: /Фото.*загружается/ }),
  ).toBeVisible();

  // После polling — реальный текст и img.
  await expect(page.getByText('Краткая биография автора из Wikipedia.')).toBeVisible({
    timeout: 10_000,
  });
  const photo = page.getByRole('img', { name: /Фото:/, exact: false });
  await expect(photo).toBeVisible();
  await expect(photo).toHaveAttribute('src', '/api/covers/author-photo.jpg');
});

test('author page: long bio shows "Развернуть" toggle, expands on click', async ({
  page,
}) => {
  await page.route('**/api/auth/me', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: {
          id: 1,
          email: 'tester@example.com',
          display_name: 'Tester',
          role: 'admin',
          created_at: '2026-05-10T00:00:00Z',
        },
      }),
    }),
  );
  await page.route(/\/api\/covers\//, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'image/png',
      body: Buffer.from(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=',
        'base64',
      ),
    }),
  );

  // Очень длинное bio (~2k символов) — гарантированно >5 строк даже
  // на дефолтном Playwright viewport (1280×720). 700 символов уложилось
  // в 5 строк на широком экране — clamp не сработал.
  const longBio = Array.from({ length: 6 })
    .map(
      () =>
        'Джаспер Ффорде — британский писатель валлийского происхождения. ' +
        'Родился в Лондоне в 1961 году. До своего успеха в литературе ' +
        'работал в киноиндустрии в качестве оператора и оператора-постановщика. ' +
        'Дебютный роман вышел в 2001 году и привёл к успеху серии. ' +
        'Известен своим юмористическим стилем и литературной игрой. ' +
        'Живёт в Уэльсе.',
    )
    .join(' ') + ' ' + 'УНИКАЛЬНЫЙ-МАРКЕР-КОНЦА-ТЕКСТА.';
  await page.route(/\/api\/authors\/17$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...authorDetailFixture, bio: longBio, photo_path: 'p.jpg' }),
    }),
  );

  await page.goto('/authors/17');

  // bio видим (видна хотя бы первая фраза).
  await expect(page.getByText(/Джаспер Ффорде/)).toBeVisible({ timeout: 10_000 });

  // Кнопка "Развернуть" должна появиться (bio > 5 строк).
  const expandBtn = page.getByRole('button', { name: 'Развернуть' });
  await expect(expandBtn).toBeVisible();

  await expandBtn.click();
  await expect(page.getByRole('button', { name: 'Свернуть' })).toBeVisible();
  // После разворота виден маркер конца текста, скрытый при clamp'е.
  await expect(page.getByText(/УНИКАЛЬНЫЙ-МАРКЕР-КОНЦА-ТЕКСТА/)).toBeVisible();
});
