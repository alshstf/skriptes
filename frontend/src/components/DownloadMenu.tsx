import { Download } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';

const formats: Array<{ id: string; label: string; sub: string }> = [
  { id: 'epub3', label: 'EPUB 3', sub: 'универсальный, для большинства ридеров и Send-to-Kindle' },
  { id: 'kepub', label: 'KEPUB', sub: 'для Kobo' },
  { id: 'azw8', label: 'AZW8', sub: 'для современных Kindle' },
  { id: 'kfx', label: 'KFX', sub: 'тоже Kindle (новая модель)' },
  { id: 'epub2', label: 'EPUB 2', sub: 'старые читалки' },
  { id: 'fb2', label: 'FB2', sub: 'оригинал, без конвертации' },
];

export function DownloadMenu({ bookId }: { bookId: number }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="default" size="sm" className="gap-2">
          <Download className="size-4" aria-hidden />
          Скачать
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel>Формат</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {formats.map((f) => (
          <DropdownMenuItem
            key={f.id}
            asChild
            // a-tag c download — браузер запустит скачивание; auth-cookie
            // уезжает автоматически (cookie HttpOnly + same-origin).
          >
            <a
              href={`/api/books/${bookId}/download?format=${f.id}`}
              download
              className="flex items-start gap-1 cursor-pointer"
            >
              <span className="font-medium">{f.label}</span>
              <span className="ml-auto text-xs text-muted-foreground">{f.sub}</span>
            </a>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
