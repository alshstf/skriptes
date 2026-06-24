import { test, expect } from './_fixtures';

// Обложка книги грузится по РЕГЕНЕРИРУЮЩЕМУ эндпоинту /api/covers/book/{editionId}
// (а не by-name /api/covers/{cover_path}). Это переживает очистку/LRU-эвикцию кэша:
// by-name отдаёт только кэш-файл и 404-ит после эвикции, а by-id переизвлекает из
// fb2 на лету. Поэтому polling-swap по cover_path для карточки больше не нужен —
// обложка запрашивается сразу.

test('book detail: cover renders via on-demand by-id endpoint', async ({
  mockedPage: page,
}) => {
  await page.goto('/books/19');
  const cover = page.getByRole('img', { name: /Обложка: Кадетский корпус/ });
  await expect(cover).toBeVisible({ timeout: 10_000 });
  // У фикстуры нет editions → coverEditionId = book.id (19). Запрос by-id, не by-name.
  await expect(cover).toHaveAttribute('src', '/api/covers/book/19');
});

test('book detail: placeholder when cover endpoint 404s', async ({ mockedPage: page }) => {
  // by-id вернул 404 (обложка не извлекается) → BookCover onError → плейсхолдер,
  // а не битая картинка. Маршрут регистрируем после mockApi → он приоритетнее.
  await page.route(/\/api\/covers\/book\//, (route) =>
    route.fulfill({ status: 404, contentType: 'text/plain', body: 'no cover' }),
  );
  await page.goto('/books/19');
  // Плейсхолдер 'icon' имеет aria-label «Обложка: … (загружается)».
  const placeholder = page.getByRole('img', { name: /Обложка: Кадетский корпус.*загружается/ });
  await expect(placeholder).toBeVisible({ timeout: 10_000 });
});
