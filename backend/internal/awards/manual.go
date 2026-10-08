package awards

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Русские премии, которых нет на Фантлабе, — список лауреатов в репозитории
// (manual.json), собранный по русской Википедии (2026-10-08): «Дар» — победители
// и читательское голосование (Мария Галина в 2025 от победы отказалась — её нет);
// «Просветитель» — основные номинации и «Перевод», без спецпремий («ПолитПросвет»,
// Digital); Премия Андрея Белого — поэзия, проза, гуманитарные исследования
// (вручается автору). Обновлять руками раз в год, после объявления лауреатов.

//go:embed manual.json
var manualJSON []byte

// manualPages — страница премии, на которую ведёт строка лауреата.
var manualPages = map[string]string{
	"dar":         "https://ru.wikipedia.org/wiki/Дар_(премия)",
	"prosvetitel": "https://ru.wikipedia.org/wiki/Просветитель_(премия)",
	"bely":        "https://ru.wikipedia.org/wiki/Премия_Андрея_Белого",
}

type manualWin struct {
	Award      string `json:"award"`
	Year       int    `json:"year"`
	Nomination string `json:"nomination"`
	Title      string `json:"title"`
	Author     string `json:"author"`
}

// manualWins — лауреаты премии из списка; номинации — в порядке появления.
func manualWins(a Award) ([]win, error) {
	var all []manualWin
	if err := json.Unmarshal(manualJSON, &all); err != nil {
		return nil, fmt.Errorf("manual awards: %w", err)
	}
	order := map[string]int{}
	var out []win
	for _, m := range all {
		if m.Award != a.Key {
			continue
		}
		if _, ok := order[m.Nomination]; !ok {
			order[m.Nomination] = len(order)
		}
		x := win{year: m.Year, nomination: m.Nomination, nomOrder: order[m.Nomination], author: strings.TrimSpace(m.Author),
			title: strings.TrimSpace(m.Title), link: manualPages[a.Key]}
		x.kind = "work"
		if a.AuthorLevel {
			x.kind, x.title = "author", ""
		}
		x.ref = fmt.Sprintf("%s/%d/%s/%s/%s", a.Key, m.Year, m.Nomination, x.title, x.author)
		out = append(out, x)
	}
	return out, nil
}
