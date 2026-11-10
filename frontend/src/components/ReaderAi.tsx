import { useState, type FormEvent } from 'react';
import { AlertTriangle, ArrowUp, Loader2, MapPin, NotebookPen } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Input } from '@/components/ui/input';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { isShortSelection, type AiPlace, type AiQuote } from '@/lib/readerAi';
import type { useAiConversation } from '@/lib/readerAiConversation';

/**
 * AiSheet — шторка «Спросить ИИ» в ридере.
 *
 * selection — выделенный фрагмент (вопросы о нём); page — видимая страница
 * («Напомнить, что было»). Пока читатель сам не выбрал действие, запросов нет:
 * каждое действие стоит денег и отправляет текст книги внешней модели.
 */
export function AiSheet({
  open,
  onOpenChange,
  conversation,
  selection,
  page,
  onGotoQuote,
  onSaveNote,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  conversation: ReturnType<typeof useAiConversation>;
  selection: AiPlace | null;
  page: AiPlace | null;
  onGotoQuote: (q: AiQuote) => void;
  /** Сохранить ответ заметкой к выделению; нет — кнопки нет. */
  onSaveNote?: (text: string) => void;
}) {
  const { items, ask, busy } = conversation;
  const [question, setQuestion] = useState('');
  const place = selection ?? page;
  const last = items[items.length - 1];
  const canFollowUp = last?.status === 'done' && last.mode !== 'translate' && last.mode !== 'define';
  const short = selection ? isShortSelection(selection.excerpt) : false;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const q = question.trim();
    if (!q || !place || busy) return;
    ask(last?.mode === 'recap' || !selection ? 'recap' : 'explain', place, q);
    setQuestion('');
  };

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        overlayClassName="bg-black/10"
        className="max-h-[80dvh] gap-0 p-0 sm:mx-auto sm:max-w-xl sm:rounded-t-xl sm:border-x"
      >
        <SheetHeader className="border-b border-border pb-3">
          <SheetTitle>Спросить ИИ</SheetTitle>
          <SheetDescription className="line-clamp-2 text-pretty">
            {selection ? `«${selection.excerpt}»` : 'По прочитанной части книги.'}
          </SheetDescription>
        </SheetHeader>

        <div className="flex-1 space-y-4 overflow-y-auto p-4" aria-live="polite">
          {items.length === 0 ? (
            <div className="flex flex-wrap gap-2">
              {selection ? (
                <>
                  <Button size="sm" onClick={() => ask('explain', selection)}>
                    Объяснить
                  </Button>
                  {short ? (
                    <>
                      <Button size="sm" variant="outline" onClick={() => ask('who', selection)}>
                        Кто это
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => ask('define', selection)}>
                        Что значит
                      </Button>
                    </>
                  ) : null}
                  <Button size="sm" variant="outline" onClick={() => ask('translate', selection)}>
                    Перевести
                  </Button>
                </>
              ) : page ? (
                <Button size="sm" onClick={() => ask('recap', page)}>
                  Напомнить, что было
                </Button>
              ) : null}
            </div>
          ) : null}

          {items.map((x, i) => (
            <div key={i} className="space-y-2">
              <p className="text-sm font-medium text-pretty">{x.label}</p>
              {x.answer ? <p className="text-sm text-pretty whitespace-pre-wrap">{x.answer}</p> : null}
              {x.status === 'asking' ? (
                <p className="flex items-center gap-2 text-xs text-muted-foreground">
                  <Loader2 className="size-3.5 animate-spin" aria-hidden />
                  {x.answer ? 'Пишу…' : x.stage >= 2 ? 'Ищу в прочитанном…' : 'Читаю…'}
                </p>
              ) : null}
              {x.status === 'error' ? (
                <Callout icon={<AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>{x.error}</Callout>
              ) : null}
              {x.truncated ? <p className="text-xs text-muted-foreground">Ответ получился длинным и был обрезан.</p> : null}
              {x.quotes.length > 0 ? (
                <ul className="space-y-1">
                  {x.quotes.map((q, k) => (
                    <li key={k}>
                      <button
                        type="button"
                        className="flex w-full items-start gap-2 rounded-md border border-border px-2 py-1.5 text-left text-xs text-muted-foreground hover:text-foreground"
                        onClick={() => onGotoQuote(q)}
                      >
                        <MapPin className="mt-0.5 size-3.5 shrink-0" aria-hidden />
                        <span className="line-clamp-2 text-pretty">Показать место: «{q.text}»</span>
                      </button>
                    </li>
                  ))}
                </ul>
              ) : null}
              {x.status === 'done' && onSaveNote && i === items.length - 1 ? (
                <Button size="sm" variant="ghost" className="gap-1.5 px-2" onClick={() => onSaveNote(x.answer)}>
                  <NotebookPen className="size-3.5" aria-hidden />
                  Сохранить в заметки
                </Button>
              ) : null}
            </div>
          ))}
        </div>

        <div className="space-y-2 border-t border-border p-3 pb-[calc(0.75rem+env(safe-area-inset-bottom))]">
          {place && (items.length === 0 || canFollowUp) ? (
            <form className="flex gap-2" onSubmit={submit}>
              <Input
                value={question}
                onChange={(e) => setQuestion(e.target.value)}
                maxLength={500}
                placeholder={items.length === 0 ? 'Свой вопрос об этом месте' : 'Спросить ещё'}
                aria-label={items.length === 0 ? 'Свой вопрос об этом месте' : 'Спросить ещё'}
              />
              <Button type="submit" size="icon" disabled={busy || !question.trim()} aria-label="Спросить">
                <ArrowUp className="size-4" aria-hidden />
              </Button>
            </form>
          ) : null}
          <p className="text-xs text-pretty text-muted-foreground">
            Ответ составляет языковая модель по прочитанной вами части книги и может ошибаться. Этот текст книги
            отправляется внешнему сервису.
          </p>
        </div>
      </SheetContent>
    </Sheet>
  );
}
