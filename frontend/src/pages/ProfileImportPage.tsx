import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { Download, FileUp, History, Undo2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Callout } from "@/components/ui/callout";
import { Skeleton } from "@/components/ui/skeleton";
import { LoadError } from "@/components/LoadError";
import { ProfileTabs } from "@/components/ProfileTabs";
import { ApiError } from "@/lib/api";
import { formatRelativeDays, pluralBooks, pluralRu } from "@/lib/format";
import {
  IMPORT_SOURCES,
  importWhat,
  sourceLabel,
  useApplyImport,
  useImportBatches,
  usePreviewImport,
  useUndoImport,
  type ImportBatch,
  type ImportCandidate,
  type ImportChoice,
  type ImportPreview,
  type ImportPreviewItem,
  type ImportReport,
  type ImportSource,
} from "@/lib/import";

/**
 * ProfileImportPage — «Импорт» (/me/import, план recsys/04): история чтения из
 * файла, который пользователь выгрузил сам. Шаги: файл → предпросмотр
 * сопоставления (уверенные — списком с галочками, спорные — выбор из
 * кандидатов, ненайденные — справкой) → применить. Своё не перетирается;
 * импорт целиком отменяется в списке ниже. Файл на сервере не хранится.
 */
export function ProfileImportPage() {
  const [file, setFile] = useState<File | null>(null);
  const [source, setSource] = useState<ImportSource | "">("");
  const [report, setReport] = useState<ImportReport | null>(null);
  const preview = usePreviewImport();
  const apply = useApplyImport();

  return (
    <article className="space-y-6">
      <ProfileTabs />
      <header className="max-w-2xl space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">
          Импорт истории чтения
        </h1>
        <p className="text-sm text-pretty text-muted-foreground">
          Перенесите оценки, прочитанное, «хочу прочитать» и подчёркивания из
          файла, который выгрузили сами, — рекомендации сразу начнут их
          учитывать. Ваши оценки и отметки не перезапишутся, а любой импорт
          можно отменить. Сам файл после разбора нигде не хранится.
        </p>
      </header>

      <section className="max-w-2xl space-y-2" aria-labelledby="import-sources">
        <h2 id="import-sources" className="text-sm font-medium">
          Откуда взять файл
        </h2>
        <ul className="space-y-1.5 text-sm text-pretty">
          {IMPORT_SOURCES.map((s) => (
            <li key={s.value}>
              <span className="font-medium">{s.label}.</span>{" "}
              <span className="text-muted-foreground">{s.hint}</span>
            </li>
          ))}
        </ul>
        <a
          href="/api/me/import/template.csv"
          download
          className="inline-flex items-center gap-1.5 text-sm underline-offset-2 hover:underline"
        >
          <Download className="size-4" aria-hidden />
          Скачать шаблон таблицы (CSV)
        </a>
        <p className="text-sm text-pretty text-muted-foreground">
          В обратную сторону —{" "}
          <a
            href="/api/me/import/export.csv"
            download
            className="text-foreground underline underline-offset-2"
          >
            ваша история из этой библиотеки
          </a>{" "}
          (оценки, прочитанное, полки, подчёркивания) в том же формате: её можно
          импортировать на другой сервер или открыть в таблице.
        </p>
      </section>

      <form
        className="max-w-2xl space-y-3 rounded-md border border-border p-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (!file) return;
          setReport(null);
          preview.mutate({ file, source });
        }}
      >
        <label className="block space-y-1 text-sm">
          <span className="font-medium">Файл</span>
          <input
            type="file"
            accept=".txt,.csv,text/plain,text/csv"
            className="block w-full text-sm text-muted-foreground file:mr-3 file:rounded-md file:border-0 file:bg-secondary file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-secondary-foreground"
            onChange={(e) => {
              setFile(e.target.files?.[0] ?? null);
              preview.reset();
              setReport(null);
            }}
          />
        </label>
        <label className="block space-y-1 text-sm">
          <span className="font-medium">Формат</span>
          <select
            value={source}
            onChange={(e) => {
              setSource(e.target.value as ImportSource | "");
              preview.reset();
            }}
            className="block h-9 w-full rounded-md border border-input bg-background px-2 text-sm sm:w-72"
          >
            <option value="">Определить по файлу</option>
            {IMPORT_SOURCES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </select>
        </label>
        <Button
          type="submit"
          size="sm"
          className="gap-1.5"
          disabled={!file || preview.isPending}
        >
          <FileUp className="size-4" aria-hidden />
          {preview.isPending ? "Разбираем…" : "Разобрать и сопоставить"}
        </Button>
        {preview.error ? (
          <p role="alert" className="text-sm text-destructive">
            {preview.error instanceof ApiError
              ? preview.error.message
              : "Не удалось разобрать файл"}
          </p>
        ) : null}
      </form>

      {report ? <ReportCallout report={report} /> : null}

      {preview.data && file ? (
        <PreviewView
          key={preview.data.file_name + preview.data.items.length}
          data={preview.data}
          busy={apply.isPending}
          onCancel={() => preview.reset()}
          onApply={(choices, watch) =>
            apply.mutate(
              { file, source: preview.data!.source, choices, watch },
              {
                onSuccess: (rep) => {
                  setReport(rep);
                  preview.reset();
                  toast.success("Импорт применён");
                },
                onError: (err) =>
                  toast.error(
                    err instanceof ApiError
                      ? err.message
                      : "Не удалось применить импорт",
                  ),
              },
            )
          }
        />
      ) : null}

      <Batches />
    </article>
  );
}

function ReportCallout({ report }: { report: ImportReport }) {
  return (
    <Callout
      className="max-w-2xl text-sm"
      icon={<History className="mt-0.5 size-4 shrink-0" aria-hidden />}
    >
      <p className="font-medium text-foreground">
        Импортировано {report.applied} {pluralBooks(report.applied)}.
      </p>
      <p>{counts(report)}</p>
      <p className="mt-1">
        Учтено в рекомендациях на{" "}
        <Link to="/" className="underline underline-offset-2">
          Главной
        </Link>
        .
      </p>
    </Callout>
  );
}

function counts(r: ImportReport): string {
  const parts = [
    r.ratings ? `оценок — ${r.ratings}` : "",
    r.reads ? `прочитано — ${r.reads}` : "",
    r.to_read ? `«хочу прочитать» — ${r.to_read}` : "",
    r.highlights ? `цитат — ${r.highlights}` : "",
    r.wishlist ? `ждём появления — ${r.wishlist}` : "",
    r.kept ? `уже было своё — ${r.kept}` : "",
  ].filter(Boolean);
  return parts.length ? parts.join(" · ") : "нового ничего — всё уже было";
}

/** PreviewView — что нашлось в библиотеке и что применить. */
function PreviewView({
  data,
  busy,
  onApply,
  onCancel,
}: {
  data: ImportPreview;
  busy: boolean;
  onApply: (choices: ImportChoice[], watch: number[]) => void;
  onCancel: () => void;
}) {
  const high = data.items.filter((it) => it.match.confidence === "high");
  const medium = data.items.filter((it) => it.match.confidence === "medium");
  const none = data.items.filter((it) => it.match.confidence === "none");
  // Уверенные — по умолчанию отмечены; спорные — не выбраны, пока не выберете.
  const [excluded, setExcluded] = useState<Set<number>>(() => new Set());
  const [picked, setPicked] = useState<Map<number, number>>(() => new Map());
  // Ненайденные — в список желаний (#597): появятся при обновлении библиотеки —
  // Главная скажет «Появилось то, что вы искали». По умолчанию — следить.
  const [watchMissing, setWatchMissing] = useState(true);
  const watch = watchMissing ? none.map((it) => it.i) : [];

  const choices = useMemo(() => {
    const out: ImportChoice[] = [];
    for (const it of high)
      if (!excluded.has(it.i) && it.match.work_id)
        out.push({ i: it.i, work_id: it.match.work_id });
    for (const [i, work_id] of picked) out.push({ i, work_id });
    return out;
  }, [high, excluded, picked]);

  return (
    <section className="max-w-2xl space-y-4" aria-labelledby="import-preview">
      <div className="space-y-1">
        <h2 id="import-preview" className="text-lg font-semibold">
          {sourceLabel(data.source)}: {data.items.length}{" "}
          {pluralBooks(data.items.length)} в файле
        </h2>
        <p
          className="text-sm text-muted-foreground"
          data-testid="import-summary"
        >
          в библиотеке уверенно — {data.summary.high} · на выбор —{" "}
          {data.summary.medium} · не нашлось — {data.summary.none}
          {data.skipped_total
            ? ` · пропущено строк — ${data.skipped_total}`
            : ""}
        </p>
      </div>

      {medium.length > 0 ? (
        <div className="space-y-2">
          <h3 className="text-sm font-medium">Выберите, какая это книга</h3>
          <ul
            className="divide-y divide-border rounded-md border border-border"
            data-testid="import-medium"
          >
            {medium.map((it) => (
              <MediumRow
                key={it.i}
                item={it}
                value={picked.get(it.i) ?? 0}
                onChange={(workId) =>
                  setPicked((m) => {
                    const next = new Map(m);
                    if (workId) next.set(it.i, workId);
                    else next.delete(it.i);
                    return next;
                  })
                }
              />
            ))}
          </ul>
        </div>
      ) : null}

      {high.length > 0 ? (
        <details
          className="group rounded-md border border-border"
          open={high.length <= 10}
        >
          <summary className="cursor-pointer px-3 py-2 text-sm font-medium">
            Нашлись уверенно — {high.length}
            <span className="font-normal text-muted-foreground">
              {" "}
              · снимите галочку, если книга не та
            </span>
          </summary>
          <ul
            className="divide-y divide-border border-t border-border"
            data-testid="import-high"
          >
            {high.map((it) => (
              <li key={it.i}>
                <label className="flex cursor-pointer items-start gap-3 px-3 py-2 text-sm">
                  <input
                    type="checkbox"
                    className="mt-1 size-4 shrink-0 accent-foreground"
                    checked={!excluded.has(it.i)}
                    onChange={(e) =>
                      setExcluded((s) => {
                        const next = new Set(s);
                        if (e.target.checked) next.delete(it.i);
                        else next.add(it.i);
                        return next;
                      })
                    }
                  />
                  <span className="min-w-0 flex-1">
                    <FileBook item={it} />
                    {it.match.candidates?.[0] ? (
                      <CandidateLine c={it.match.candidates[0]} arrow />
                    ) : null}
                  </span>
                </label>
              </li>
            ))}
          </ul>
        </details>
      ) : null}

      {none.length > 0 ? (
        <details className="rounded-md border border-border">
          <summary className="cursor-pointer px-3 py-2 text-sm font-medium">
            Нет в библиотеке — {none.length}
          </summary>
          <ul
            className="space-y-1 border-t border-border px-3 py-2 text-sm"
            data-testid="import-none"
          >
            {none.map((it) => (
              <li key={it.i}>
                <FileBook item={it} />
              </li>
            ))}
          </ul>
        </details>
      ) : null}

      {none.length > 0 ? (
        <label className="flex cursor-pointer items-start gap-3 text-sm">
          <input
            type="checkbox"
            className="mt-0.5 size-4 shrink-0 accent-foreground"
            checked={watchMissing}
            onChange={(e) => setWatchMissing(e.target.checked)}
          />
          <span className="text-pretty">
            Следить за ненайденными — когда книга появится в библиотеке, Главная
            скажет «Появилось то, что вы искали»
          </span>
        </label>
      ) : null}

      {data.skipped_total > 0 ? (
        <details className="rounded-md border border-border">
          <summary className="cursor-pointer px-3 py-2 text-sm font-medium">
            Пропущены строки файла — {data.skipped_total}
          </summary>
          <ul className="space-y-1 border-t border-border px-3 py-2 text-xs text-muted-foreground">
            {data.skipped.map((s, k) => (
              <li key={k}>
                {s.line ? `строка ${s.line}: ` : ""}
                {s.reason}
              </li>
            ))}
            {data.skipped_total > data.skipped.length ? (
              <li>и ещё {data.skipped_total - data.skipped.length}</li>
            ) : null}
          </ul>
        </details>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <Button
          disabled={busy || (choices.length === 0 && watch.length === 0)}
          onClick={() => onApply(choices, watch)}
        >
          {busy
            ? "Применяем…"
            : choices.length > 0
              ? `Применить — ${choices.length} ${pluralBooks(choices.length)}`
              : `Следить — ${watch.length} ${pluralBooks(watch.length)}`}
        </Button>
        <Button variant="ghost" onClick={onCancel} disabled={busy}>
          Отмена
        </Button>
      </div>
    </section>
  );
}

/** FileBook — книга, как она записана в файле, и что из неё возьмём. */
function FileBook({ item }: { item: ImportPreviewItem }) {
  const what = importWhat(item);
  return (
    <span className="block">
      <span className="font-medium">{item.title}</span>
      {item.authors.length ? (
        <span className="text-muted-foreground">
          {" "}
          — {item.authors.join(", ")}
        </span>
      ) : null}
      {what ? (
        <span className="block text-xs text-muted-foreground">{what}</span>
      ) : null}
    </span>
  );
}

function CandidateLine({
  c,
  arrow = false,
}: {
  c: ImportCandidate;
  arrow?: boolean;
}) {
  return (
    <span className="block text-xs text-pretty">
      {arrow ? "→ " : ""}
      <span className="font-medium">{c.title}</span>
      {c.authors.length ? ` — ${c.authors.join(", ")}` : ""}
      {c.year ? `, ${c.year}` : ""}
    </span>
  );
}

function MediumRow({
  item,
  value,
  onChange,
}: {
  item: ImportPreviewItem;
  value: number;
  onChange: (workId: number) => void;
}) {
  const name = `import-pick-${item.i}`;
  return (
    <li className="space-y-1.5 px-3 py-2 text-sm">
      <FileBook item={item} />
      <fieldset
        className="space-y-1"
        aria-label={`Какая это книга: ${item.title}`}
      >
        {(item.match.candidates ?? []).map((c) => (
          <label
            key={c.work_id}
            className="flex cursor-pointer items-start gap-2"
          >
            <input
              type="radio"
              name={name}
              className="mt-0.5 size-4 shrink-0 accent-foreground"
              checked={value === c.work_id}
              onChange={() => onChange(c.work_id)}
            />
            <CandidateLine c={c} />
          </label>
        ))}
        <label className="flex cursor-pointer items-start gap-2 text-xs text-muted-foreground">
          <input
            type="radio"
            name={name}
            className="mt-0.5 size-4 shrink-0 accent-foreground"
            checked={value === 0}
            onChange={() => onChange(0)}
          />
          Ни одна — не импортировать
        </label>
      </fieldset>
    </li>
  );
}

/** Batches — прошлые импорты с отменой. */
function Batches() {
  const q = useImportBatches();
  const undo = useUndoImport();
  const [confirm, setConfirm] = useState<number | null>(null);
  const items = q.data?.items ?? [];
  if (q.isLoading) return <Skeleton className="h-16 w-full max-w-2xl" />;
  if (q.error)
    return (
      <LoadError error={q.error}>
        Не удалось загрузить прошлые импорты.
      </LoadError>
    );
  if (items.length === 0) return null;
  return (
    <section className="max-w-2xl space-y-2" aria-labelledby="import-batches">
      <h2 id="import-batches" className="text-sm font-medium">
        Прошлые импорты
      </h2>
      <ul
        className="divide-y divide-border rounded-md border border-border"
        data-testid="import-batches"
      >
        {items.map((b) => (
          <li key={b.batch_id} className="space-y-1 px-3 py-2 text-sm">
            <div className="flex items-start gap-3">
              <div className="min-w-0 flex-1">
                <p className="truncate">
                  <span className="font-medium">{sourceLabel(b.source)}</span>
                  {b.file_name ? (
                    <span className="text-muted-foreground">
                      {" "}
                      · {b.file_name}
                    </span>
                  ) : null}
                </p>
                <p className="text-xs text-muted-foreground">
                  {formatRelativeDays(b.applied_at)} · {b.applied} из {b.items}{" "}
                  {pluralBooks(b.items)} · {counts(b)}
                </p>
                {b.undone_at ? (
                  <p className="text-xs text-muted-foreground">
                    отменён {formatRelativeDays(b.undone_at)}
                  </p>
                ) : null}
              </div>
              {!b.undone_at && confirm !== b.batch_id ? (
                <Button
                  size="sm"
                  variant="outline"
                  className="shrink-0 gap-1"
                  onClick={() => setConfirm(b.batch_id)}
                >
                  <Undo2 className="size-4" aria-hidden />
                  Отменить
                </Button>
              ) : null}
            </div>
            {confirm === b.batch_id ? (
              <UndoConfirm
                batch={b}
                busy={undo.isPending}
                onCancel={() => setConfirm(null)}
                onConfirm={() =>
                  undo.mutate(b.batch_id, {
                    onSuccess: () => toast.success("Импорт отменён"),
                    onError: () => toast.error("Не удалось отменить импорт"),
                    onSettled: () => setConfirm(null),
                  })
                }
              />
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}

function UndoConfirm({
  batch,
  busy,
  onConfirm,
  onCancel,
}: {
  batch: ImportBatch;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const total =
    batch.ratings +
    batch.reads +
    batch.to_read +
    batch.highlights +
    (batch.wishlist ?? 0);
  return (
    <div className="flex flex-wrap items-center gap-2 pt-1">
      <span className="text-xs text-pretty text-muted-foreground">
        Удалить {total} {pluralRu(total, ["запись", "записи", "записей"])} этого
        импорта? То, что вы потом меняли сами, останется.
      </span>
      <Button
        size="sm"
        variant="destructive"
        disabled={busy}
        onClick={onConfirm}
      >
        Отменить импорт
      </Button>
      <Button size="sm" variant="ghost" onClick={onCancel}>
        Нет
      </Button>
    </div>
  );
}
