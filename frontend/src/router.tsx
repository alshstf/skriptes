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

const booksRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/books',
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

const routeTree = rootRoute.addChildren([
  loginRoute,
  protectedRoute.addChildren([indexRoute, booksRoute, bookDetailRoute, authorRoute, seriesRoute]),
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
