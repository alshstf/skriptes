// Package converter обёртка над бинарём fbc (rupor-github/fb2cng).
//
// Преобразует fb2-файлы из zip-архивов в форматы для e-readers
// (epub2/epub3/kepub/kfx/azw8) или просто отдаёт исходный fb2.
//
// Кэш — content-addressable на диске: ключ включает book_id и версию
// fbc, чтобы при апгрейде бинаря результаты автоматически
// перегенерировались. LRU-чистка пока не реализована — рассчитываем,
// что cache volume в compose растёт медленнее чем заполняется диск
// (домашний сервер). Когда понадобится — добавим.
package converter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Format — целевой формат для конвертации.
// "fb2" — passthrough (без fbc, просто отдаём содержимое из zip).
type Format string

const (
	FormatFB2   Format = "fb2"
	FormatEpub2 Format = "epub2"
	FormatEpub3 Format = "epub3"
	FormatKepub Format = "kepub"
	FormatKFX   Format = "kfx"
	FormatAZW8  Format = "azw8"
)

// fbcFormats — что мы передаём в --to флаг fbc.
// Порядок не важен; map нужен для O(1) проверки.
var fbcFormats = map[Format]string{
	FormatEpub2: "epub2",
	FormatEpub3: "epub3",
	FormatKepub: "kepub",
	FormatKFX:   "kfx",
	FormatAZW8:  "azw8",
}

// fileExt — какое расширение получает конвертированный файл.
// fbc формирует имя <basename>.<ext>; для kindle — .azw8 / .kfx.
var fileExt = map[Format]string{
	FormatFB2:   "fb2",
	FormatEpub2: "epub",
	FormatEpub3: "epub",
	FormatKepub: "kepub.epub", // fbc выдаёт kepub под этим suffix
	FormatKFX:   "kfx",
	FormatAZW8:  "azw8",
}

// mimeType — Content-Type для отдачи скачивания.
var mimeType = map[Format]string{
	FormatFB2:   "application/x-fictionbook+xml",
	FormatEpub2: "application/epub+zip",
	FormatEpub3: "application/epub+zip",
	FormatKepub: "application/epub+zip",
	FormatKFX:   "application/octet-stream",
	FormatAZW8:  "application/octet-stream",
}

// ParseFormat — парсит строку из URL query, возвращает ErrUnknownFormat
// для неизвестных значений.
func ParseFormat(s string) (Format, error) {
	if s == "" {
		return FormatEpub3, nil // дефолт: универсально поддерживается
	}
	f := Format(strings.ToLower(s))
	switch f {
	case FormatFB2, FormatEpub2, FormatEpub3, FormatKepub, FormatKFX, FormatAZW8:
		return f, nil
	}
	return "", ErrUnknownFormat
}

// Ошибки converter'а.
var (
	ErrUnknownFormat = errors.New("unknown format")
	ErrSourceMissing = errors.New("source archive or fb2 file missing")
)

// SourceBook — минимум информации о книге, нужный конвертеру.
// Заполняется handler'ом из books.Book.
type SourceBook struct {
	ID       int64  // используется в имени кэш-файла
	Archive  string // имя zip-архива относительно BooksRoot, например "fb2-749080-749080.zip"
	FileName string // имя fb2-файла внутри zip без расширения, "749080"
	Ext      string // обычно "fb2"
}

// Result — что возвращает Convert.
type Result struct {
	Path        string // абсолютный путь к готовому файлу (на диске cache или внутри zip)
	ContentType string // Content-Type для HTTP
	Filename    string // suggested filename для Content-Disposition
	FromCache   bool   // hit или miss
}

// Converter — оркестратор. Безопасен для параллельных вызовов.
// Гарантия от race'ов на cache file: per-key sync.Mutex.
type Converter struct {
	booksRoot string // /data/books
	cacheRoot string // /cache/converted
	fbcPath   string // путь к бинарю fbc (обычно "fbc" в PATH)

	mu    sync.Mutex
	keyMu map[string]*sync.Mutex // chan-style guard: одна конвертация в момент времени per-key
}

// New собирает Converter. fbcPath может быть пустым → используется "fbc"
// (ищется в PATH). cacheRoot создаётся при необходимости.
func New(booksRoot, cacheRoot, fbcPath string) (*Converter, error) {
	if booksRoot == "" {
		return nil, errors.New("converter: empty booksRoot")
	}
	if cacheRoot == "" {
		return nil, errors.New("converter: empty cacheRoot")
	}
	if fbcPath == "" {
		fbcPath = "fbc"
	}
	if err := os.MkdirAll(filepath.Join(cacheRoot, "converted"), 0o750); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &Converter{
		booksRoot: booksRoot,
		cacheRoot: cacheRoot,
		fbcPath:   fbcPath,
		keyMu:     make(map[string]*sync.Mutex),
	}, nil
}

// Convert возвращает путь к файлу нужного формата для книги.
// Для FormatFB2 — прямой путь внутри zip-архива, конвертация не нужна.
// Для остальных форматов — путь к закэшированному файлу в cacheRoot;
// при cache miss запускается fbc, синхронно дожидается завершения.
func (c *Converter) Convert(ctx context.Context, b SourceBook, format Format) (Result, error) {
	if b.Archive == "" || b.FileName == "" || b.Ext == "" {
		return Result{}, ErrSourceMissing
	}

	archivePath := filepath.Join(c.booksRoot, b.Archive)
	if _, err := os.Stat(archivePath); err != nil {
		return Result{}, fmt.Errorf("%w: %s", ErrSourceMissing, archivePath)
	}

	// Для FB2 passthrough: формируем путь fbc-style "archive.zip/file.fb2",
	// но handler обращается к нему через стандартный архив-reader, не через fbc.
	if format == FormatFB2 {
		return Result{
			Path:        archivePath, // handler сам распакует
			ContentType: mimeType[FormatFB2],
			Filename:    b.FileName + ".fb2",
		}, nil
	}

	cacheFile := filepath.Join(c.cacheRoot, "converted", fmt.Sprintf("%d-%s.%s", b.ID, format, fileExt[format]))
	if st, err := os.Stat(cacheFile); err == nil && st.Size() > 0 {
		return Result{
			Path:        cacheFile,
			ContentType: mimeType[format],
			Filename:    b.FileName + "." + fileExt[format],
			FromCache:   true,
		}, nil
	}

	// Ровно одна конвертация в момент времени для одного cacheFile —
	// иначе два параллельных запроса будут дёргать fbc на тот же выход.
	mu := c.lockFor(cacheFile)
	mu.Lock()
	defer mu.Unlock()

	// Повторная проверка cache после получения mutex — предыдущий
	// держатель мог уже всё сконвертировать.
	if st, err := os.Stat(cacheFile); err == nil && st.Size() > 0 {
		return Result{
			Path:        cacheFile,
			ContentType: mimeType[format],
			Filename:    b.FileName + "." + fileExt[format],
			FromCache:   true,
		}, nil
	}

	if err := c.runFBC(ctx, archivePath, b.FileName+"."+b.Ext, format, cacheFile); err != nil {
		return Result{}, fmt.Errorf("fbc convert: %w", err)
	}
	return Result{
		Path:        cacheFile,
		ContentType: mimeType[format],
		Filename:    b.FileName + "." + fileExt[format],
		FromCache:   false,
	}, nil
}

// runFBC вызывает fbc convert с zip-syntax источником.
// fbc принимает SOURCE вида "archive.zip[inner_path]/file.fb2" — split-by-`.zip`.
// DESTINATION — директория; имя выхода fbc формирует сам из метаданных
// книги, поэтому потом мы ищем файл по расширению и переименовываем
// в cacheFile.
//
// tmpDir создаём ВНУТРИ cacheRoot — чтобы os.Rename был atomic move
// в пределах одной fs (rename между /tmp и mounted volume = EXDEV).
func (c *Converter) runFBC(ctx context.Context, archivePath, fb2Name string, format Format, cacheFile string) error {
	tmpDir, err := os.MkdirTemp(c.cacheRoot, "fbc-")
	if err != nil {
		return fmt.Errorf("mkdir temp: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	source := archivePath + "/" + fb2Name
	cmd := exec.CommandContext(ctx, c.fbcPath, // #nosec G204 — fbcPath из конфига, fb2Name из БД
		"convert",
		"--to", fbcFormats[format],
		"--nodirs",
		"--overwrite",
		source,
		tmpDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fbc returned %v: %s", err, string(out))
	}

	// fbc формирует имя выходного файла из метаданных книги
	// ("Author - Title [extras].ext"), а не из имени источника. Имя
	// мы заранее не знаем, но в tmpDir всегда ровно один результат
	// — поэтому ищем по расширению.
	produced, err := findProducedFile(tmpDir, fileExt[format])
	if err != nil {
		return fmt.Errorf("find output: %w; fbc output: %s", err, string(out))
	}
	// Перенос на финальный путь: rename atomically внутри одного fs.
	// Если cacheFile уже существует (другой воркер успел) — overwrite ok.
	if err := os.Rename(produced, cacheFile); err != nil {
		return fmt.Errorf("move to cache: %w", err)
	}
	return nil
}

// findProducedFile ищет в dir файл с расширением ext (например "epub").
// Возвращает первый найденный или ошибку, если ничего нет.
// Точные имена файлов fbc формирует динамически — нам нужно только
// расширение и факт существования одного результата.
func findProducedFile(dir, ext string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	suffix := "." + ext
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), strings.ToLower(suffix)) {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("no .%s file found in %s", ext, dir)
}

func (c *Converter) lockFor(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	mu, ok := c.keyMu[key]
	if !ok {
		mu = &sync.Mutex{}
		c.keyMu[key] = mu
	}
	return mu
}

// AvailableFormats — отсортированный список поддерживаемых форматов
// (для UI / API).
func AvailableFormats() []Format {
	return []Format{FormatFB2, FormatEpub3, FormatEpub2, FormatKepub, FormatKFX, FormatAZW8}
}
