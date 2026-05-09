import { useQuery } from '@tanstack/react-query';
import { BookOpen } from 'lucide-react';

type Health = { status: string };

function App() {
  const { data, isLoading, error } = useQuery<Health>({
    queryKey: ['healthz'],
    queryFn: async () => {
      const r = await fetch('/healthz');
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      return r.json() as Promise<Health>;
    },
    retry: 1,
  });

  return (
    <main className="min-h-dvh grid place-items-center p-6">
      <div className="max-w-md w-full space-y-6 text-center">
        <div className="flex items-center justify-center gap-3">
          <BookOpen className="size-8 text-primary" />
          <h1 className="text-3xl font-semibold tracking-tight">skriptes</h1>
        </div>
        <p className="text-muted-foreground">Каталогизатор домашней библиотеки</p>
        <div className="rounded-lg border border-border p-4 text-sm">
          <div className="text-xs uppercase tracking-wider text-muted-foreground mb-2">Backend status</div>
          {isLoading && <span className="text-muted-foreground">проверяем…</span>}
          {error && <span className="text-destructive">недоступен ({(error as Error).message})</span>}
          {data && <span className="text-green-500">{data.status}</span>}
        </div>
      </div>
    </main>
  );
}

export default App;
