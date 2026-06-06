// downloadFormats — форматы конвертации для меню «Скачать» (общие для
// DownloadMenu и EditionActions). Вынесено в отдельный модуль, чтобы файлы-
// компоненты экспортировали только компоненты (react-refresh).
export const downloadFormats: Array<{ id: string; label: string; sub: string }> = [
  { id: 'epub3', label: 'EPUB 3', sub: 'универсальный, для большинства ридеров и Send-to-Kindle' },
  { id: 'kepub', label: 'KEPUB', sub: 'для Kobo' },
  { id: 'azw8', label: 'AZW8', sub: 'для современных Kindle' },
  { id: 'kfx', label: 'KFX', sub: 'тоже Kindle (новая модель)' },
  { id: 'epub2', label: 'EPUB 2', sub: 'старые читалки' },
  { id: 'fb2', label: 'FB2', sub: 'оригинал, без конвертации' },
];
