import { useMe } from '@/lib/auth';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';

export function HomePage() {
  const me = useMe();
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Добро пожаловать{me.data ? `, ${me.data.display_name}` : ''}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            Списки книг, авторов и поиск появятся в следующем PR.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
