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
import { BooksPage } from '@/pages/BooksPage';
import { BookDetailPage } from '@/pages/BookDetailPage';
import { AuthorPage } from '@/pages/AuthorPage';
import { SeriesPage } from '@/pages/SeriesPage';
import { ProfilePage } from '@/pages/ProfilePage';
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

// Защищённое поддерево: всё что внутри protectedRoute требует валидной
// сессии. beforeLoad дёргает /me ОДИН раз через QueryClient (если в кэше
// уже есть — без сетевого запроса) и редиректит на /login, если нет.
const protectedRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'protected',
  beforeLoad: async ({ context }) => {
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
  },
  component: () => (
    <Layout>
      <Outlet />
    </Layout>
  ),
});

// '/' редиректит на /books — главной страницы пока нет, список книг
// и есть точка входа в каталог.
const indexRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/books' });
  },
  // Component обязан быть, но не рендерится из-за redirect.
  component: () => null,
});

// BooksSearch — URL-стейт списка книг.
// Все поля опциональные; пустые/нулевые значения вырезаются из URL,
// чтобы /books выглядел чистым без активных фильтров.
export type BooksSearch = {
  q?: string;
  page?: number;
  genres?: string[];
  lang?: string;
  year_from?: number;
  year_to?: number;
  series_id?: number;
  author_id?: number;
  sort?: 'year_desc' | 'year_asc' | 'popularity';
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
      year_from: asNumber(search.year_from),
      year_to: asNumber(search.year_to),
      series_id: asNumber(search.series_id),
      author_id: asNumber(search.author_id),
      sort:
        sort === 'year_desc' || sort === 'year_asc' || sort === 'popularity' ? sort : undefined,
    };
  },
  component: BooksPage,
});

const bookDetailRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/books/$id',
  component: BookDetailPage,
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

const profileRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/me',
  component: ProfilePage,
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  protectedRoute.addChildren([
    indexRoute,
    booksRoute,
    bookDetailRoute,
    authorRoute,
    seriesRoute,
    profileRoute,
  ]),
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
