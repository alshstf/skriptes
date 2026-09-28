// Package langcode — канонизация кода языка из fb2 (<src-lang>) к ISO 639-1.
//
// В fb2 пишут что угодно: ISO 639-2 (spa, ger, eng), коды стран вместо языков
// (jp, gr, cz, ua), слова по-болгарски и по-русски («английски», «русский»),
// мусор («?», «u»). В фильтре «Язык оригинала» это давало ~60 лишних значений и
// дубли одного языка (прод 2026-09: spa 240, ger 75, «английски» 50, jp 100, #287).
package langcode

import "strings"

// iso6391 — действующие коды ISO 639-1 (+ устаревший, но осмысленный sh —
// сербохорватский: во что его превращать, неизвестно).
var iso6391 = setOf(`aa ab ae af ak am an ar as av ay az ba be bg bh bi bm bn bo br bs ca ce ch co cr cs cu cv cy
da de dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht hu hy hz ia id ie ig ii
ik io is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr
ms mt my na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pi pl ps pt qu rm rn ro ru rw sa sc sd se sg sh si sk
sl sm sn so sq sr ss st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh yi yo za
zh zu`)

// noISO6391 — коды ISO 639-2/3 языков, у которых двухбуквенного кода нет
// (древнегреческий, якутский, удмуртский…): оставляем как есть, имя языку даёт
// Intl.DisplayNames на фронте.
var noISO6391 = setOf(`ang akk arc azb chm cop egy enm fro frm gmh goh grc kbd krc lzh mdf mhr myv non ota pal
peo sah sux syc tyv udm xal`)

// aliases — ISO 639-2 (B и T), устаревшие и ошибочные коды, названия словами.
var aliases = map[string]string{
	// ISO 639-2/B и 639-2/T.
	"afr": "af", "alb": "sq", "sqi": "sq", "amh": "am", "ara": "ar", "arm": "hy", "hye": "hy", "aze": "az",
	"baq": "eu", "eus": "eu", "bel": "be", "ben": "bn", "bod": "bo", "tib": "bo", "bos": "bs", "bul": "bg",
	"cat": "ca", "ces": "cs", "cze": "cs", "chi": "zh", "zho": "zh", "chu": "cu", "chv": "cv", "cym": "cy",
	"wel": "cy", "dan": "da", "deu": "de", "ger": "de", "dut": "nl", "nld": "nl", "ell": "el", "gre": "el",
	"eng": "en", "epo": "eo", "est": "et", "fas": "fa", "per": "fa", "fin": "fi", "fra": "fr", "fre": "fr",
	"geo": "ka", "kat": "ka", "gle": "ga", "glg": "gl", "heb": "he", "hin": "hi", "hrv": "hr", "hun": "hu",
	"ice": "is", "isl": "is", "ita": "it", "jpn": "ja", "kaz": "kk", "kir": "ky", "kor": "ko", "lat": "la",
	"lav": "lv", "lit": "lt", "mac": "mk", "mkd": "mk", "mon": "mn", "nor": "no", "pol": "pl", "por": "pt",
	"ron": "ro", "rum": "ro", "rus": "ru", "san": "sa", "slk": "sk", "slo": "sk", "slv": "sl", "spa": "es",
	"srp": "sr", "swe": "sv", "tat": "tt", "tgk": "tg", "tuk": "tk", "tur": "tr", "ukr": "uk", "urd": "ur",
	"uzb": "uz", "vie": "vi", "yid": "yi",
	// Устаревшие коды ISO 639-1.
	"iw": "he", "in": "id", "ji": "yi", "jw": "jv", "mo": "ro",
	// Коды стран вместо кодов языков.
	"jp": "ja", "gr": "el", "cz": "cs", "dk": "da", "by": "be", "ua": "uk", "tm": "tk",
	// Названия словами (в fb2 встречаются по-болгарски, по-русски, по-английски).
	"английски": "en", "английский": "en", "english": "en",
	"български": "bg", "болгарский": "bg",
	"руски": "ru", "русский": "ru", "рус": "ru", "russian": "ru",
	"френски": "fr", "французский": "fr", "french": "fr",
	"немски": "de", "немецкий": "de", "german": "de",
	"испански": "es", "испанский": "es", "spanish": "es",
	"шведски": "sv", "шведский": "sv", "турски": "tr", "турецкий": "tr",
	"японский": "ja", "фарси": "fa",
	"украинский": "uk", "ukrain": "uk", "urkain": "uk", "ukrainian": "uk",
	"turkmen": "tk", "uzbek": "uz", "balkar": "krc",
}

// Canonical приводит код языка к ISO 639-1 (или к коду 639-2/3 языка, у
// которого двухбуквенного нет). Пустая строка — код не распознан: такое значение
// не записываем (лучше «неизвестно», чем мусор в фильтре). Регион/письменность
// отрезаются: «ru-RU», «zh_Hans» → «ru», «zh».
func Canonical(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexAny(s, "-_"); i >= 0 {
		s = s[:i]
	}
	if v, ok := aliases[s]; ok {
		return v
	}
	if iso6391[s] || noISO6391[s] {
		return s
	}
	return ""
}

func setOf(list string) map[string]bool {
	m := map[string]bool{}
	for _, c := range strings.Fields(list) {
		m[c] = true
	}
	return m
}
