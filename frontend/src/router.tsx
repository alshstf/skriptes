import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Navigate,
  Outlet,
  redirect,
} from '@tanstack/react-router';
import type { QueryClient } from '@tanstack/react-query';
import { Layout } from '@/components/Layout';
import { LoginPage } from '@/pages/LoginPage';
import { HomePage } from '@/pages/HomePage';
import { BooksPage } from '@/pages/BooksPage';
import { BookDetailPage } from '@/pages/BookDetailPage';
import { AuthorsPage } from '@/pages/AuthorsPage';
import { AuthorPage } from '@/pages/AuthorPage';
import { SeriesPage } from '@/pages/SeriesPage';
import { GenresPage } from '@/pages/GenresPage';
import { ShelvesPage } from '@/pages/ShelvesPage';
import { ProfilePage } from '@/pages/ProfilePage';
import { ProfileContentPage } from '@/pages/ProfileContentPage';
import { ProfileAppearancePage } from '@/pages/ProfileAppearancePage';
import { ReaderPage } from '@/pages/ReaderPage';
import { AdminGeneralPage } from '@/pages/AdminGeneralPage';
import { AdminUsersPage } from '@/pages/AdminUsersPage';
import { AdminContentPage } from '@/pages/AdminContentPage';
import { AdminBackgroundPage } from '@/pages/AdminBackgroundPage';
import { apiFetch, ApiError } from '@/lib/api';
import type { MeResponse } from '@/lib/auth';

// RouterContext предоставляет beforeLoad-у доступ к QueryClient — чтобы
// proactively проверить /me и сделать redirect ДО рендера, без вспышки
// неавторизованного UI.
type RouterContext = {
  queryClient: QueryClient;
};

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: () => <Outlet />,
  notFoundComponent: () => <Navigate to="/" />,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
});

// requireAuth — общий beforeLoad для всех защищённых веток: дёргает /me
// один раз через QueryClient (cache hit = без сетевого) и редиректит на
// /login если не авторизован.
async function requireAuth(context: RouterContext) {
  const me = await context.queryClient.fetchQuery({
    queryKey: ['auth', 'me'],
    queryFn: async () => {
      try {
        const r = await apiFetch<MeResponse>('/api/auth/me');
        return r.user;
      } catch (err) {
        if (err instanceof ApiError && err.isUnauthorized()) return null;
        throw err;
      }
    },
    staleTime: 60_000,
  });
  if (!me) {
    throw redirect({ to: '/login' });
  }
}

// Защищённое поддерево с обычным Layout (header + сайдбары).
const protectedRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'protected',
  beforeLoad: ({ context }) => requireAuth(context),
  component: () => (
    <Layout>
      <Outlet />
    </Layout>
  ),
});

// Защищённое full-screen поддерево БЕЗ Layout — используется для
// ридера, где header / sidebar мешают погружению в чтение. Тот же
// requireAuth, но Layout не оборачивает.
const protectedFullscreenRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'protected-fullscreen',
  beforeLoad: ({ context }) => requireAuth(context),
  component: () => <Outlet />,
});

// '/' — Главная: доминанта hero-поиск + динамические блоки (продолжить
// чтение, новинки по подпискам). Раньше редиректила на /books.
const indexRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/',
  component: HomePage,
});

// BooksSearch — URL-стейт списка книг.
// Все поля опциональные; пустые/нулевые значения вырезаются из URL,
// чтобы /books выглядел чистым без активных фильтров.
export type BooksSearch = {
  q?: string;
  page?: number;
  genres?: string[];
  lang?: string;
  src_lang?: string; // язык ОРИГИНАЛА (fb2 src-lang), независим от языка издания
  year_from?: number;
  year_to?: number;
  series_id?: number;
  author_id?: number;
  sort?: 'year_desc' | 'year_asc';
};

// AuthorsSearch — URL-стейт списка авторов (раздел «Авторы»). Как BooksSearch:
// все поля опциональные, пустые/нулевые/дефолтные вырезаются из URL. Нужно,
// чтобы фильтры/поиск/сортировка переживали уход на карточку автора и возврат
// назад (раньше были в локальном стейте и сбрасывались). sort='name' — дефолт,
// в URL не пишем.
export type AuthorsSearch = {
  q?: string;
  genres?: string[];
  langs?: string[]; // язык ИЗДАНИЯ (books.lang)
  src_langs?: string[]; // язык ОРИГИНАЛА (books.src_lang) — независимый фильтр
  year_from?: number;
  year_to?: number;
  has_adaptations?: boolean;
  min_rating?: number;
  min_reader_rating?: number;
  favorites_only?: boolean;
  sort?: 'book_count' | 'rating' | 'reader_rating';
};

function asString(v: unknown): string | undefined {
  return typeof v === 'string' && v !== '' ? v : undefined;
}
function asNumber(v: unknown): number | undefined {
  const n = typeof v === 'number' ? v : Number(v);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}
function asStringArray(v: unknown): string[] | undefined {
  if (Array.isArray(v)) {
    const out = v.filter((x): x is string => typeof x === 'string' && x !== '');
    return out.length > 0 ? out : undefined;
  }
  if (typeof v === 'string' && v !== '') {
    const out = v.split(',').filter(Boolean);
    return out.length > 0 ? out : undefined;
  }
  return undefined;
}
// asBool — только true сохраняем в URL; false/отсутствие → undefined (вырезаем).
function asBool(v: unknown): boolean | undefined {
  return v === true || v === 'true' ? true : undefined;
}

export const booksRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/books',
  validateSearch: (search: Record<string, unknown>): BooksSearch => {
    const sort = asString(search.sort);
    return {
      q: asString(search.q),
      page: asNumber(search.page),
      genres: asStringArray(search.genres),
      lang: asString(search.lang),
      src_lang: asString(search.src_lang),
      year_from: asNumber(search.year_from),
      year_to: asNumber(search.year_to),
      series_id: asNumber(search.series_id),
      author_id: asNumber(search.author_id),
      // 'popularity' больше не значение UI: дефолтный порядок и так
      // популярность-ordered (старые URL молча падают в дефолт — порядок тот же).
      sort: sort === 'year_desc' || sort === 'year_asc' ? sort : undefined,
    };
  },
  component: BooksPage,
});

// /books/$id — карточка по id ИЗДАНИЯ (обратная совместимость: прямые ссылки,
// возврат из ридера). Ссылки из списков ведут на /works/$id (ниже).
const bookDetailRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/books/$id',
  component: BookDetailPage,
});

// /works/$id — карточка логической книги по works.id (основной маршрут карточки).
const workDetailRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/works/$id',
  component: () => <BookDetailPage mode="work" />,
});

// /authors — список авторов с фильтрами (раздел «Авторы»). Отдельный маршрут
// от /authors/$id (карточка одного автора) — статический путь vs. path-параметр.
const authorsListRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/authors',
  validateSearch: (search: Record<string, unknown>): AuthorsSearch => {
    const sort = asString(search.sort);
    return {
      q: asString(search.q),
      genres: asStringArray(search.genres),
      langs: asStringArray(search.langs),
      src_langs: asStringArray(search.src_langs),
      year_from: asNumber(search.year_from),
      year_to: asNumber(search.year_to),
      has_adaptations: asBool(search.has_adaptations),
      min_rating: asNumber(search.min_rating),
      min_reader_rating: asNumber(search.min_reader_rating),
      favorites_only: asBool(search.favorites_only),
      sort:
        sort === 'book_count' || sort === 'rating' || sort === 'reader_rating' ? sort : undefined,
    };
  },
  component: AuthorsPage,
});

const authorRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/authors/$id',
  component: AuthorPage,
});

const seriesRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/series/$id',
  component: SeriesPage,
});

// /genres — раздел «Жанры»: обзор жанров с избранным (личные полки — на /shelves).
const genresRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/genres',
  component: GenresPage,
});

// /shelves — личные полки (коллекции). Личная библиотека, не каталог-браузинг,
// поэтому не в топ-навигации, а доступом из меню пользователя.
const shelvesRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/shelves',
  component: ShelvesPage,
});

const profileRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/me',
  component: ProfilePage,
});

const profileContentRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/me/content',
  component: ProfileContentPage,
});

const profileAppearanceRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/me/appearance',
  component: ProfileAppearancePage,
});

// requireAdmin — расширение requireAuth с проверкой role на клиенте.
// Backend всё равно гейтит 403'м, но клиентский redirect даёт лучший
// UX (юзер не видит вспышки страницы, на которую у него нет прав).
async function requireAdmin(context: RouterContext) {
  await requireAuth(context);
  const me = context.queryClient.getQueryData<{ role?: string } | null>(['auth', 'me']);
  if (!me || me.role !== 'admin') {
    throw redirect({ to: '/books' });
  }
}

const adminGeneralRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/admin/general',
  beforeLoad: ({ context }) => requireAdmin(context),
  component: AdminGeneralPage,
});

const adminUsersRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/admin/users',
  beforeLoad: ({ context }) => requireAdmin(context),
  component: AdminUsersPage,
});

const adminContentRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/admin/content',
  beforeLoad: ({ context }) => requireAdmin(context),
  component: AdminContentPage,
});

const adminBackgroundRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/admin/background',
  beforeLoad: ({ context }) => requireAdmin(context),
  component: AdminBackgroundPage,
});

// Reader живёт в full-screen ветке (без Layout) — ему нужен весь
// viewport для iframe-ридера. Auth по-прежнему обязателен.
const readerRoute = createRoute({
  getParentRoute: () => protectedFullscreenRoute,
  path: '/books/$id/read',
  component: ReaderPage,
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  protectedRoute.addChildren([
    indexRoute,
    booksRoute,
    bookDetailRoute,
    workDetailRoute,
    authorsListRoute,
    authorRoute,
    seriesRoute,
    genresRoute,
    shelvesRoute,
    profileRoute,
    profileContentRoute,
    profileAppearanceRoute,
    adminGeneralRoute,
    adminUsersRoute,
    adminContentRoute,
    adminBackgroundRoute,
  ]),
  protectedFullscreenRoute.addChildren([readerRoute]),
]);

export function createAppRouter(queryClient: QueryClient) {
  return createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: 'intent',
  });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
