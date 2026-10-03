import { Link } from '@tanstack/react-router';

/**
 * AdminTabs — подвкладки раздела «Администрирование» в виде segmented-
 * control (контейнер-«таблетка», активная вкладка — залитый фон + тень).
 * Явно читается как переключатель разделов. На узком экране вкладки
 * прокручиваются внутри строки, а не растягивают страницу.
 */
const tabs = [
  { to: '/admin/general', label: 'Общее' },
  { to: '/admin/users', label: 'Пользователи' },
  { to: '/admin/content', label: 'Контент' },
  { to: '/admin/authors', label: 'Дубли авторов' },
  { to: '/admin/background', label: 'Фоновые операции' },
] as const;

const baseTab = 'shrink-0 whitespace-nowrap rounded-md px-3 py-1.5 text-sm font-medium transition';

export function AdminTabs() {
  return (
    <nav
      className="inline-flex max-w-full items-center gap-1 overflow-x-auto rounded-lg border border-border bg-muted p-1"
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
