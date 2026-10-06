import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

// Состав сборников (#388): «Состав» у сборника (совпавшие строки — ссылками,
// длинный состав свёрнут), «Входит в сборники» у произведения и «Прочитано в
// сборнике» в блоке «Моё» — рядом с «Отметить прочитанной», не вместо неё.

const contents = Array.from({ length: 18 }, (_, i) =>
  i === 0 ? { title: 'Сестры', work_id: 501 } : i === 2 ? { title: 'Аравия', work_id: 503 } : { title: `Рассказ ${i + 1}` },
);

test('сборник: «Состав» со ссылками и «Показать все»', async ({ mockedPage: page }) => {
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, title: 'Дублинцы', contents }),
    }),
  );
  await page.goto('/books/19');
  const section = page.getByRole('region', { name: /Состав/ });
  await expect(section).toBeVisible({ timeout: 10_000 });
  await expect(section.getByRole('link', { name: 'Сестры' })).toHaveAttribute('href', '/works/501');
  await expect(section.getByRole('link', { name: 'Аравия' })).toHaveAttribute('href', '/works/503');
  // Строка без работы — текст, не ссылка.
  await expect(section.getByText('Рассказ 2')).toBeVisible();
  await expect(section.getByRole('link', { name: 'Рассказ 2' })).toHaveCount(0);
  // Свёрнуто до 15 строк.
  await expect(section.getByRole('listitem')).toHaveCount(15);
  await section.getByRole('button', { name: 'Показать все (18)' }).click();
  await expect(section.getByRole('listitem')).toHaveCount(18);
});

test('произведение: «Входит в сборники» и «Прочитано в сборнике»', async ({ mockedPage: page }) => {
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ...bookDetailFixture,
        title: 'Мертвые',
        is_read: false,
        in_compilations: [
          { work_id: 900, title: 'Дублинцы', kind: 'collection', author: 'Джойс Джеймс' },
          { work_id: 901, title: 'Ирландская новелла', kind: 'anthology' },
        ],
        read_in_compilation: { work_id: 900, title: 'Дублинцы' },
      }),
    }),
  );
  await page.goto('/books/19');
  const section = page.getByRole('region', { name: /Входит в сборники/ });
  await expect(section).toBeVisible({ timeout: 10_000 });
  await expect(section.getByRole('link', { name: 'Дублинцы' })).toHaveAttribute('href', '/works/900');
  await expect(section.getByText(/сборник · Джойс Джеймс/)).toBeVisible();
  await expect(section.getByText(/антология/)).toBeVisible();

  // Явной отметки нет — кнопка «Отметить прочитанной» на месте, рядом отметка.
  await expect(page.getByRole('button', { name: /Отметить прочитанной/ })).toBeVisible();
  await expect(page.getByText(/Прочитано в сборнике/)).toBeVisible();
  await expect(page.getByRole('link', { name: '«Дублинцы»' })).toHaveAttribute('href', '/works/900');
});

test('обычная книга без состава и сборников — блоков нет', async ({ mockedPage: page }) => {
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(bookDetailFixture) }),
  );
  await page.goto('/books/19');
  await expect(page.getByText('Ваша оценка:')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('region', { name: /Состав/ })).toHaveCount(0);
  await expect(page.getByRole('region', { name: /Входит в сборники/ })).toHaveCount(0);
  await expect(page.getByText(/Прочитано в сборнике/)).toHaveCount(0);
});
