import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { Toaster } from 'sonner';
import { createAppRouter } from './router';
import { InstallPromptBanner } from './components/InstallPromptBanner';
import { applyPendingUpdate, installPromptStore, registerPWA } from './lib/pwa';
import './index.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, refetchOnWindowFocus: false, retry: 1 },
  },
});

const router = createAppRouter(queryClient);
// Вышла новая версия, пока приложение открыто: переход на другую страницу —
// момент её применить (см. onNewVersion в lib/pwa).
router.subscribe('onBeforeNavigate', (e) => {
  applyPendingUpdate(e.toLocation.href, e.pathChanged);
});

// PWA: подписываемся на beforeinstallprompt ДО регистрации SW — событие
// может прилететь сразу после bootstrap. Сам SW регистрируем после
// первого рендера (immediate=true внутри registerPWA), чтобы не задержать
// initial paint.
installPromptStore.init();
void registerPWA();

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <InstallPromptBanner />
      <Toaster richColors position="top-right" />
    </QueryClientProvider>
  </StrictMode>,
);
