import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "./api";
import { pluralRu } from "./format";

/**
 * import.ts — импорт истории чтения из файла (план recsys/04, 4.4): Kindle «My
 * Clippings.txt», CSV-каталог calibre, Goodreads «Export Library», наш
 * CSV-шаблон. Два шага без хранения файла на сервере: preview — разбор и
 * сопоставление с библиотекой, apply — тот же файл и выбор пользователя. Пакет
 * импорта можно отменить целиком (удаляется только записанное им).
 */

export type ImportSource = "kindle" | "calibre" | "goodreads" | "csv";

export const IMPORT_SOURCES: {
  value: ImportSource;
  label: string;
  hint: string;
}[] = [
  {
    value: "kindle",
    label: "Kindle",
    hint: "Файл documents/My Clippings.txt на самом Kindle (подключите по USB): подчёркивания и заметки.",
  },
  {
    value: "calibre",
    label: "calibre",
    hint: "Преобразовать книги → Создать каталог книг → CSV: оценки; «прочитано» — из колонки #read, если она есть.",
  },
  {
    value: "goodreads",
    label: "Goodreads",
    hint: "My Books → Import and export → Export Library: оценки, прочитанное, «to-read». Так же — LiveLib через Livelib Backup в режиме для Goodreads.",
  },
  {
    value: "csv",
    label: "Таблица по шаблону",
    hint: "Для всего остального (Яндекс Книги, бумажные списки): заполните шаблон — название, автор, оценка 1–5, дата прочтения, полка, цитата.",
  },
];

export const sourceLabel = (s: string) =>
  IMPORT_SOURCES.find((x) => x.value === s)?.label ?? s;

export type ImportCandidate = {
  work_id: number;
  title: string;
  authors: string[];
  year?: number;
};

export type ImportMatch = {
  confidence: "high" | "medium" | "none";
  work_id?: number;
  candidates?: ImportCandidate[];
};

export type ImportPreviewItem = {
  i: number;
  title: string;
  authors: string[];
  rating?: number;
  read: boolean;
  read_at?: string;
  shelf?: "read" | "to_read" | "reading";
  highlights: number;
  line: number;
  match: ImportMatch;
};

export type ImportPreview = {
  source: ImportSource;
  file_name: string;
  items: ImportPreviewItem[];
  summary: { high: number; medium: number; none: number };
  skipped: { line: number; reason: string }[];
  skipped_total: number;
};

export type ImportReport = {
  batch_id: number;
  applied: number;
  ratings: number;
  reads: number;
  to_read: number;
  highlights: number;
  kept: number;
  // ненайденных, взятых в список желаний (#597): появятся — «Появилось то, что вы искали»
  wishlist: number;
};

export type ImportBatch = ImportReport & {
  source: ImportSource;
  file_name: string;
  items: number;
  applied_at: string;
  undone_at?: string;
};

export type ImportChoice = { i: number; work_id: number };

export const IMPORT_BATCHES_KEY = ["me", "import", "batches"] as const;

function form(
  file: File,
  source: ImportSource | "",
  choices?: ImportChoice[],
  watch?: number[],
): FormData {
  const fd = new FormData();
  if (source) fd.append("source", source);
  if (choices) fd.append("choices", JSON.stringify(choices));
  if (watch?.length) fd.append("watch", JSON.stringify(watch));
  fd.append("file", file, file.name);
  return fd;
}

export function usePreviewImport() {
  return useMutation({
    mutationFn: ({ file, source }: { file: File; source: ImportSource | "" }) =>
      apiFetch<ImportPreview>("/api/me/import/preview", {
        method: "POST",
        body: form(file, source),
      }),
  });
}

export function useApplyImport() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      file,
      source,
      choices,
      watch,
    }: {
      file: File;
      source: ImportSource;
      choices: ImportChoice[];
      // книги файла (индексы), которых нет в библиотеке, — в список желаний
      watch?: number[];
    }) =>
      apiFetch<ImportReport>("/api/me/import/apply", {
        method: "POST",
        body: form(file, source, choices, watch),
      }),
    onSuccess: () => invalidateAfterImport(qc),
  });
}

export function useImportBatches() {
  return useQuery<{ items: ImportBatch[] }>({
    queryKey: IMPORT_BATCHES_KEY,
    queryFn: ({ signal }) =>
      apiFetch<{ items: ImportBatch[] }>("/api/me/import/batches", { signal }),
    staleTime: 10_000,
  });
}

export function useUndoImport() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      apiFetch<void>(`/api/me/import/batches/${id}`, { method: "DELETE" }),
    onSuccess: () => invalidateAfterImport(qc),
  });
}

// Импорт меняет оценки, прочитанное, полки и заметки — от них зависит почти
// всё (карточки, лента, полки, статистика); операция редкая — сбрасываем кэш
// целиком.
function invalidateAfterImport(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries();
}

/** Что из книги файла пойдёт в библиотеку — одной строкой. */
export function importWhat(it: ImportPreviewItem): string {
  const parts: string[] = [];
  if (it.rating) parts.push(`оценка ${it.rating}`);
  if (it.read)
    parts.push(it.read_at ? `прочитано ${formatDay(it.read_at)}` : "прочитано");
  else if (it.rating) parts.push("прочитано");
  if (it.shelf === "to_read" && !it.read && !it.rating)
    parts.push("хочу прочитать");
  if (it.highlights)
    parts.push(
      `${it.highlights} ${pluralRu(it.highlights, ["цитата", "цитаты", "цитат"])}`,
    );
  return parts.join(" · ");
}

function formatDay(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? ""
    : d.toLocaleDateString("ru-RU", { timeZone: "UTC" });
}
