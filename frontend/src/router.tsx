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

const homeRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/',
  component: HomePage,
});

const routeTree = rootRoute.addChildren([loginRoute, protectedRoute.addChildren([homeRoute])]);

export function createAppRouter(queryClient: QueryClient) {
  return createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: 'intent',
  });
}

// Регистрируем тип роутера для type-safe Link/useNavigate.
declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
