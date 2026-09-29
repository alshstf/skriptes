import { Copy, Info } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';

/**
 * KindleSenderHint — адрес, с которого сервер отправляет книги, и короткая
 * подсказка, как разрешить его в Amazon (выжимка раздела 1 инструкции в README,
 * «Send-to-Kindle → для читателей»). Подсказка — поповер по нажатию, а не
 * тултип: тултип на телефоне не открывается.
 *
 * sender пуст — SMTP на сервере не настроен: отправка недоступна, об этом и пишем.
 */
export function KindleSenderHint({ sender }: { sender: string }) {
  if (!sender) {
    return (
      <p className="text-sm text-muted-foreground">
        Отправка на Kindle на сервере пока не настроена — обратитесь к администратору.
      </p>
    );
  }

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(sender);
      toast.success('Адрес отправителя скопирован');
    } catch {
      toast.error('Не удалось скопировать — выделите адрес вручную');
    }
  };

  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
        <span className="text-muted-foreground">Отправитель книг:</span>
        <code className="break-all rounded bg-muted px-1.5 py-0.5 font-mono text-xs select-all">
          {sender}
        </code>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-7"
          onClick={copy}
          aria-label="Скопировать адрес отправителя"
          title="Скопировать"
        >
          <Copy className="size-3.5" aria-hidden />
        </Button>
      </div>
      <Popover>
        <PopoverTrigger asChild>
          <button
            type="button"
            className="inline-flex items-center gap-1 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            <Info className="size-3.5" aria-hidden />
            Как разрешить его в Amazon
          </button>
        </PopoverTrigger>
        <PopoverContent className="w-80 text-sm">
          <p className="mb-2 font-medium text-pretty">
            Amazon принимает книги только от адресов, которые вы разрешили.
          </p>
          <ol className="list-decimal space-y-1.5 pl-4 text-muted-foreground">
            <li>
              Откройте{' '}
              <a
                href="https://www.amazon.com/mycd"
                target="_blank"
                rel="noreferrer"
                className="text-foreground underline underline-offset-2"
              >
                amazon.com/mycd
              </a>{' '}
              — или /mycd магазина, где зарегистрирован Kindle (amazon.de и т. п.).
            </li>
            <li>
              <b className="text-foreground">Preferences</b> →{' '}
              <b className="text-foreground">Personal Document Settings</b>.
            </li>
            <li>
              <b className="text-foreground">Approved Personal Document E-mail List</b> →{' '}
              <b className="text-foreground">Add a new approved e-mail address</b>: вставьте{' '}
              {/* адрес и здесь: поповер может открыться поверх строки с ним */}
              <code className="break-all rounded bg-muted px-1 font-mono text-xs text-foreground">
                {sender}
              </code>{' '}
              → Add Address.
            </li>
            <li>
              Там же, в <b className="text-foreground">Send-to-Kindle E-Mail Settings</b>, —
              адрес вашего Kindle (<span className="whitespace-nowrap">…@kindle.com</span>). Его
              добавьте ниже.
            </li>
          </ol>
        </PopoverContent>
      </Popover>
    </div>
  );
}
