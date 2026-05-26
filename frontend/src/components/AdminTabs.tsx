import { Link } from '@tanstack/react-router';

/**
 * AdminTabs — подвкладки раздела «Администрирование» в виде segmented-
 * control (контейнер-«таблетка», активная вкладка — залитый фон + тень).
 * Явно читается как переключатель разделов.
 */
const tabs = [
  { to: '/admin/users', label: 'Пользователи' },
  { to: '/admin/cover-cache', label: 'Кэш обложек' },
] as const;

const baseTab = 'rounded-md px-3 py-1.5 text-sm font-medium transition';

export function AdminTabs() {
  return (
    <nav
      className="inline-flex items-center gap-1 rounded-lg border border-border bg-muted p-1"
      aria-label="Администрирование"
    >
      {tabs.map((t) => (
        <Link
          key={t.to}
          to={t.to}
          className={`${baseTab} text-muted-foreground hover:text-foreground`}
          activeProps={{ className: `${baseTab} bg-background text-foreground shadow-sm` }}
        >
          {t.label}
        </Link>
      ))}
    </nav>
  );
}
