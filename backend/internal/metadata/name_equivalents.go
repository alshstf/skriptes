package metadata

import (
	"slices"
	"strings"
)

// Русская передача западных имён ↔ оригинал (#259). Имя-гейт сравнивает имя
// транслитом с допуском в одну букву: «Уильям» → uilyam не сходится с William,
// «Стивен» → stiven — со Stephen. На проде у 2 тыс. известных переводных авторов
// без латинского имени из переводов биография была у 49 % против 81 % у тех, у
// кого оно есть. Словарь — только устойчивые передачи; фамилия по-прежнему
// обязательна, политика приёма — та же.
var firstNameEquivalentsRu = map[string][]string{
	"уильям": {"william"}, "уилльям": {"william"}, "вильям": {"william"}, "уилл": {"will"}, "билл": {"bill"},
	"стивен": {"stephen", "steven"}, "стив": {"steve"}, "джон": {"john"}, "джонатан": {"jonathan"},
	"джеймс": {"james"}, "джим": {"jim"}, "джордж": {"george"}, "джозеф": {"joseph"}, "джо": {"joe"},
	"джек": {"jack"}, "джефф": {"jeff"}, "джеффри": {"jeffrey", "geoffrey"}, "джефри": {"jeffrey", "geoffrey"},
	"джереми": {"jeremy"}, "джесси": {"jesse", "jessie"}, "джеральд": {"gerald"}, "джерри": {"jerry"},
	"джин": {"gene", "jean"}, "джейн": {"jane"}, "дженнифер": {"jennifer"}, "джессика": {"jessica"},
	"джоан": {"joan", "joanne"}, "джоанна": {"joanna"}, "джулия": {"julia"}, "джулиан": {"julian"},
	"джудит": {"judith"}, "джуди": {"judy"}, "джойс": {"joyce"}, "джозефина": {"josephine"},
	"майкл": {"michael"}, "мэри": {"mary"}, "мэтью": {"matthew"}, "мэтт": {"matt"}, "маргарет": {"margaret"},
	"мэгги": {"maggie"}, "мэделин": {"madeleine", "madeline"}, "кэтрин": {"catherine", "katherine", "kathryn"},
	"кейт": {"kate"}, "кэти": {"katie", "kathy"}, "кристофер": {"christopher"}, "кристина": {"christina", "kristina"},
	"чарльз": {"charles"}, "чак": {"chuck"}, "томас": {"thomas"}, "тони": {"tony"}, "энтони": {"anthony"},
	"эндрю": {"andrew"}, "эдвард": {"edward"}, "генри": {"henry"}, "гарри": {"harry"}, "хелен": {"helen"},
	"говард": {"howard"}, "гарольд": {"harold"}, "хью": {"hugh"}, "филип": {"philip", "phillip"},
	"филипп": {"philip", "phillip"}, "фрэнсис": {"francis", "frances"}, "фрэнк": {"frank"},
	"фредерик": {"frederick", "frederic"}, "уолтер": {"walter"}, "уоррен": {"warren"}, "уэйн": {"wayne"},
	"уэсли": {"wesley"}, "дэвид": {"david"}, "дэниел": {"daniel"}, "дэн": {"dan"}, "дуглас": {"douglas"},
	"нил": {"neil", "neal", "niall"}, "найджел": {"nigel"}, "питер": {"peter"}, "пол": {"paul"}, "кит": {"keith"},
	"брайан": {"brian", "bryan"}, "брюс": {"bruce"}, "рэй": {"ray"}, "рэймонд": {"raymond"}, "роджер": {"roger"},
	"рут": {"ruth"}, "сьюзен": {"susan"}, "сьюзан": {"susan"}, "сара": {"sarah", "sara"}, "шарлотта": {"charlotte"},
	"шон": {"sean", "shaun"}, "шерил": {"cheryl"}, "эмили": {"emily"}, "энн": {"ann", "anne"}, "элизабет": {"elizabeth"},
	"элис": {"alice"}, "эллен": {"ellen"}, "ян": {"ian", "jan"}, "иэн": {"iain", "ian"}, "айзек": {"isaac"},
	"айрис": {"iris"}, "алистер": {"alistair", "alastair"}, "артур": {"arthur"}, "терри": {"terry"}, "тед": {"ted"},
	"тимоти": {"timothy"}, "грегори": {"gregory"}, "грэм": {"graham", "graeme"}, "бенджамин": {"benjamin"},
	"клайв": {"clive"}, "клиффорд": {"clifford"}, "кэрол": {"carol"}, "кэролайн": {"caroline"},
	"николас": {"nicholas"}, "ребекка": {"rebecca"}, "рассел": {"russell"}, "саймон": {"simon"},
	"сэмюэл": {"samuel"}, "сэмюел": {"samuel"}, "сэм": {"sam"}, "стэнли": {"stanley"}, "фиби": {"phoebe"},
	"хилари": {"hilary", "hillary"}, "ширли": {"shirley"}, "дэшил": {"dashiell"}, "ли": {"lee"},
	"лоуренс": {"lawrence", "laurence"}, "луис": {"louis", "luis"}, "льюис": {"lewis"}, "люси": {"lucy"},
	"мюриэл": {"muriel"}, "нэнси": {"nancy"}, "филлис": {"phyllis"}, "энид": {"enid"}, "кеннет": {"kenneth"},
	"мэттью": {"matthew"}, "натаниэль": {"nathaniel"}, "натаниел": {"nathaniel"}, "оуэн": {"owen"},
	"патриция": {"patricia"}, "ричард": {"richard"}, "дороти": {"dorothy"}, "дебора": {"deborah"},
	"эрнест": {"ernest"}, "орсон": {"orson"}, "роберт": {"robert"}, "джулиус": {"julius"}, "эрл": {"earl"},
}

// firstNameVariants — латинские варианты имени по русской передаче (пусто —
// имени нет в словаре).
func firstNameVariants(first string) []string {
	w := strings.Fields(strings.ToLower(strings.ReplaceAll(first, "ё", "е")))
	if len(w) == 0 {
		return nil
	}
	return firstNameEquivalentsRu[w[0]]
}

// ruFormsByLatin — обратный словарь: латинское имя → транслиты его русских
// передач («william» → uilyam, uillyam, vilyam): запрос бывает латиницей (из
// переводов или словаря), а кандидат — кириллицей (издания OpenLibrary на русском).
var ruFormsByLatin = func() map[string][]string {
	out := map[string][]string{}
	for ru, vars := range firstNameEquivalentsRu {
		t := translitName(ru)
		for _, v := range vars {
			out[v] = append(out[v], t)
		}
	}
	return out
}()

// firstNameEquivalentMatches — имя кандидата — устойчивая передача имени запроса
// в другом алфавите: «William» для «Уильям» и «Уильям» для «William».
func firstNameEquivalentMatches(tokens []nameToken, first string) bool {
	if hasCyrillic(first) {
		vars := firstNameVariants(first)
		for _, t := range tokens {
			if !t.cyr && slices.Contains(vars, t.lat) {
				return true
			}
		}
		return false
	}
	w := strings.Fields(strings.ToLower(first))
	if len(w) == 0 {
		return false
	}
	forms := ruFormsByLatin[w[0]]
	for _, t := range tokens {
		if t.cyr && slices.Contains(forms, t.lat) {
			return true
		}
	}
	return false
}
