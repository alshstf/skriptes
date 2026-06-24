package metadata

// externalQueryFields — поля книги для построения ВНЕШНЕГО поискового запроса
// (cover/rating/year backfill): локализованные поля издания + оригинал из fb2
// <src-title-info> (для переводных книг).
type externalQueryFields struct {
	id            int64
	title         string   // название издания (часто перевод, напр. русское)
	lang          string   // язык издания (ISO)
	authors       []string // "Фамилия Имя [Отчество]" как в каталоге (часто кириллица)
	srcTitle      string   // оригинальное название (fb2 <src-title-info>), латиница
	srcAuthorNorm string   // оригинальный автор, нормализованный (латиница)
	srcLang       string   // язык оригинала (ISO)
}

// buildExternalQuery — BookQuery для внешних каталогов (OpenLibrary / Google Books).
//
// OL/GB индексируют по ОРИГИНАЛЬНОМУ названию и латинице. По русскому переводу +
// кириллическому «Фамилия Имя» совпадений почти нет — проверено: OL по «Еще один
// великолепный МИФ» / «Асприн Роберт» → 0, а по «Another Fine Myth» / «Robert
// Asprin» → есть. Поэтому для переводных книг (есть src_title из fb2
// <src-title-info>) ищем по ОРИГИНАЛУ: src_title + латинский автор
// (src_author_normalized, иначе транслитерация первого автора через translitName)
// + src_lang. Без src_title — по локализованным полям (как было: для книг, где
// язык издания = язык оригинала, это и есть оригинал).
// externalTitleAuthorLang — ядро выбора: для переводных книг (есть srcTitle) —
// оригинал (src_title + латинский автор + src_lang), иначе локализованные поля.
// Общее для BookQuery (cover/year) и WorkQuery (rating, где сверху ещё ISBN и
// last/first name для гейта authorNameMatches — он сам транслитерирует кириллицу).
func externalTitleAuthorLang(f externalQueryFields) (title string, authors []string, lang string) {
	if f.srcTitle != "" {
		author := f.srcAuthorNorm
		if author == "" && len(f.authors) > 0 {
			author = translitName(f.authors[0])
		}
		if author != "" {
			authors = []string{author}
		}
		return f.srcTitle, authors, f.srcLang
	}
	return f.title, f.authors, f.lang
}

func buildExternalQuery(f externalQueryFields) BookQuery {
	title, authors, lang := externalTitleAuthorLang(f)
	return BookQuery{ID: f.id, Title: title, Authors: authors, Lang: lang}
}
