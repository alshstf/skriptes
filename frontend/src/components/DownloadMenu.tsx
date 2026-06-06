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
import { downloadFormats } from '@/lib/formats';

export function DownloadMenu({ bookId }: { bookId: number }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="default" size="sm" className="gap-2">
          <Download className="size-4" aria-hidden />
          Скачать
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-80">
        <DropdownMenuLabel>Формат</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {downloadFormats.map((f) => (
          <DropdownMenuItem key={f.id} asChild>
            {/*
              Вертикальная раскладка вместо ряда: ширины меню (320px) хватает
              на короткий label (EPUB 3 / KEPUB / ...) и длинное описание
              типа "универсальный, для большинства ридеров и Send-to-Kindle"
              только если они на разных строках. Ряд с ml-auto ужимал label
              в две строки, как только описание становилось длинным —
              "EPUB 3" разъезжалось на "EPUB" / "3".
            */}
            <a
              href={`/api/books/${bookId}/download?format=${f.id}`}
              download
              className="flex flex-col items-start gap-0.5 cursor-pointer"
            >
              <span className="font-medium whitespace-nowrap">{f.label}</span>
              <span className="text-xs text-muted-foreground whitespace-normal break-words">
                {f.sub}
              </span>
            </a>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
