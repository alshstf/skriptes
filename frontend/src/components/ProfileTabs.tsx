import { Link } from '@tanstack/react-router';

/**
 * ProfileTabs — подвкладки раздела «Профиль» (segmented-control, как в
 * админке). «Профиль» (/me) и «Контент» (/me/content).
 *
 * activeOptions exact: /me — префикс /me/content, без exact обе вкладки
 * подсвечивались бы на /me/content.
 */
const tabs = [
  { to: '/me', label: 'Профиль' },
  { to: '/me/content', label: 'Контент' },
] as const;

const baseTab = 'rounded-md px-3 py-1.5 text-sm font-medium transition';

export function ProfileTabs() {
  return (
    <nav
      className="inline-flex items-center gap-1 rounded-lg border border-border bg-muted p-1"
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
