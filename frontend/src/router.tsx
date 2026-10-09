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
import { AwardPage, AwardsPage } from '@/pages/AwardsPage';
import { ShelvesPage } from '@/pages/ShelvesPage';
import { ProfilePage } from '@/pages/ProfilePage';
import { ProfileContentPage } from '@/pages/ProfileContentPage';
import { ProfileAppearancePage } from '@/pages/ProfileAppearancePage';
import { ReaderPage } from '@/pages/ReaderPage';
import { AdminGeneralPage } from '@/pages/AdminGeneralPage';
import { AdminUsersPage } from '@/pages/AdminUsersPage';
import { AdminContentPage } from '@/pages/AdminContentPage';
import { AdminAuthorDuplicatesPage } from '@/pages/AdminAuthorDuplicatesPage';
import { AdminBackgroundPage } from '@/pages/AdminBackgroundPage';
import { apiFetch, ApiError } from '@/lib/api';
import type { MeResponse } from '@/lib/auth';
import { BOOK_KINDS, type BookKind } from '@/lib/books';

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
  kind?: BookKind; // тип работы: книги или вид сборника (#379)
  year_from?: number;
  year_to?: number;
  series_id?: number;
  author_id?: number;
  sort?: 'year_desc' | 'year_asc';
  unread?: boolean; // только непрочитанные (умные полки)
  awards?: string[]; // лауреаты этих премий (#447)
  has_award?: boolean; // лауреаты любой премии
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
  has_awards?: boolean;
  min_rating?: number;
  min_reader_rating?: number;
  favorites_only?: boolean;
  // 'renown' («Сначала известные») — дефолт, в URL не живёт; 'name' — явный алфавит.
  sort?: 'name' | 'book_count' | 'rating' | 'reader_rating';
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

function asBookKind(v: unknown): BookKind | undefined {
  return typeof v === 'string' && (BOOK_KINDS as readonly string[]).includes(v) ? (v as BookKind) : undefined;
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
      kind: asBookKind(search.kind),
      year_from: asNumber(search.year_from),
      year_to: asNumber(search.year_to),
      series_id: asNumber(search.series_id),
      author_id: asNumber(search.author_id),
      // 'popularity' больше не значение UI: дефолтный порядок и так
      // популярность-ordered (старые URL молча падают в дефолт — порядок тот же).
      sort: sort === 'year_desc' || sort === 'year_asc' ? sort : undefined,
      unread: asBool(search.unread),
      awards: asStringArray(search.awards),
      has_award: asBool(search.has_award),
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
      has_awards: asBool(search.has_awards),
      min_rating: asNumber(search.min_rating),
      min_reader_rating: asNumber(search.min_reader_rating),
      favorites_only: asBool(search.favorites_only),
      sort:
        sort === 'name' || sort === 'book_count' || sort === 'rating' || sort === 'reader_rating'
          ? sort
          : undefined,
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

// /awards — раздел «Премии» (#389): премии белого списка; /awards/$key — лауреаты по годам.
const awardsRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/awards',
  component: AwardsPage,
});

const awardRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/awards/$key',
  component: AwardPage,
  // library — «Только из библиотеки» (#469: переживает «Назад» с карточки книги).
  validateSearch: (search: Record<string, unknown>): { library?: boolean } => ({ library: asBool(search.library) }),
});

// /shelves — личные полки (коллекции), готовые подборки и умные полки; пункт
// «Полки» в основной навигации (#441).
const shelvesRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/shelves',
  component: ShelvesPage,
  // open — раскрытые полки и подборки (lib/openState.ts, #469).
  validateSearch: (search: Record<string, unknown>): { open?: string[] } => ({ open: asStringArray(search.open) }),
});

const profileRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/me',
  // returnTo — куда вернуться после настройки (напр. «Настроить Kindle» с карточки
  // книги). Принимаем ТОЛЬКО внутренний путь (один ведущий '/'); protocol-relative
  // '//host' и внешние URL отбрасываем (анти-open-redirect).
  validateSearch: (search: Record<string, unknown>): { returnTo?: string } => {
    const rt = asString(search.returnTo);
    return { returnTo: rt && rt.startsWith('/') && !rt.startsWith('//') ? rt : undefined };
  },
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

const adminAuthorsRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/admin/authors',
  beforeLoad: ({ context }) => requireAdmin(context),
  component: AdminAuthorDuplicatesPage,
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
  // cfi — открыть на месте заметки («Мои заметки» на карточке, #389).
  validateSearch: (search: Record<string, unknown>): { cfi?: string } =>
    typeof search.cfi === 'string' && search.cfi ? { cfi: search.cfi } : {},
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
    awardsRoute,
    awardRoute,
    shelvesRoute,
    profileRoute,
    profileContentRoute,
    profileAppearanceRoute,
    adminGeneralRoute,
    adminUsersRoute,
    adminContentRoute,
    adminAuthorsRoute,
    adminBackgroundRoute,
  ]),
  protectedFullscreenRoute.addChildren([readerRoute]),
]);

export function createAppRouter(queryClient: QueryClient) {
  return createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: 'intent',
    // «Назад» — на ту же позицию прокрутки (#469): списки рисуются из кэша
    // react-query сразу, раскрытые полки и переключатели — в адресе.
    scrollRestoration: true,
  });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
