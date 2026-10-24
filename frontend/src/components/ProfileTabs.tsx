import { useEffect, useRef } from 'react';
import { Link } from '@tanstack/react-router';

/**
 * ProfileTabs — подвкладки раздела «Профиль» (segmented-control, как в
 * админке). «Профиль» (/me), «Контент» (/me/content), «Внешний вид», «Поиск» (/me/searches).
 *
 * activeOptions exact: /me — префикс /me/content, без exact обе вкладки
 * подсвечивались бы на /me/content. «Импорт» (/me/import) — история чтения из файла.
 */
const tabs = [
  { to: '/me', label: 'Профиль' },
  { to: '/me/content', label: 'Контент' },
  { to: '/me/appearance', label: 'Внешний вид' },
  { to: '/me/searches', label: 'Поиск' },
  { to: '/me/import', label: 'Импорт' },
] as const;

// На телефоне пять вкладок не влезают — полоса прокручивается, а не тянет страницу вбок.
const baseTab = 'shrink-0 whitespace-nowrap rounded-md px-3 py-1.5 text-sm font-medium transition';

export function ProfileTabs() {
  // Активная вкладка за краем узкой полосы — докручиваем к ней.
  const ref = useRef<HTMLElement | null>(null);
  useEffect(() => {
    ref.current?.querySelector('[aria-current="page"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }, []);
  return (
    <nav
      ref={ref}
      className="inline-flex max-w-full items-center gap-1 overflow-x-auto rounded-lg border border-border bg-muted p-1"
      aria-label="Профиль"
    >
      {tabs.map((t) => (
        <Link
          key={t.to}
          to={t.to}
          activeOptions={{ exact: true }}
          className={`${baseTab} text-muted-foreground hover:text-foreground`}
          activeProps={{ className: `${baseTab} bg-background text-foreground shadow-sm` }}
        >
          {t.label}
        </Link>
      ))}
    </nav>
  );
}
