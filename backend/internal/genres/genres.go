// Package genres держит «человеческие» имена fb2-жанров и заполняет
// ими таблицу genres при старте backend'а.
//
// Источник словаря: genres_fb2.glst из ksandr/Books.NET (он же —
// идентичный набор который используют MyHomeLib / librusec / Flibusta).
// 268 жанровых записей, ~22 верхних категории, иерархия category → genre.
//
// Скачано разово 2026-05-23 из commit e2c2e71 на ksandr/Books.NET:
//
//	https://github.com/ksandr/Books.NET/blob/master/App_Data/genres_fb2.glst
//
// Парсено в dictionary.json формата [{code, name_ru, category}].
// Жанры fb2 меняются редко (raster fb2-спека стабильна с 2010-х); если
// упустится десяток новых кодов из реальных коллекций — importer
// положит их с NULL name_ru, и UI покажет голый код как fallback.
//
// Стратегия Seed:
//  1. На startup вызываем Seed(ctx, pool) — UPSERT каждой записи из
//     словаря в genres по fb2_code.
//  2. Для уже существующих записей перезаписываем name_ru / parent_id
//     (наш словарь авторитетный).
//  3. Для новых — INSERT.
//  4. Жанры которых нет в словаре остаются как есть (см. importer:
//     он сам инсёртит их с NULL name_ru, если код пришёл из INPX и
//     отсутствует в нашем словаре).
//
// Идемпотентно — повторные вызовы безопасны.
package genres

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed dictionary.json
var rawDictionary []byte

// Entry — одна запись словаря. Парс из dictionary.json.
//
// Category — имя верхней категории как строка (например «Фантастика»).
// Не fb2_code: верхние категории fb2-спека не определяет, это
// MyHomeLib-специфичная группировка. Внутри Seed мы создаём
// «псевдо-жанр» с code = транслитом category-имени для использования
// в качестве parent_id (см. categoryCode).
type Entry struct {
	Code     string `json:"code"`
	NameRu   string `json:"name_ru"`
	Category string `json:"category"`
}

// Dictionary — все записи; парсится один раз lazy.
func Dictionary() ([]Entry, error) {
	var out []Entry
	if err := json.Unmarshal(rawDictionary, &out); err != nil {
		return nil, fmt.Errorf("parse embedded dictionary: %w", err)
	}
	return out, nil
}

// Seed — UPSERT всех known жанров с локализованными именами и
// иерархией. Возвращает количество обработанных записей (sanity-check
// для логов: если упало до десятка — что-то пошло не так с embed).
//
// Hierarchy: для каждой категории (Фантастика, Проза, …) создаём в
// genres «псевдо-родителя» с code = `cat:<slug>` (slug — наш
// transliterated key, не fb2_code). У леаф-жанра parent_id ссылается
// на этого псевдо-родителя. Это позволяет потом сгруппировать UI
// фильтр по категориям без дополнительных таблиц.
//
// Транзакционно: либо все вставки прошли, либо ни одной.
func Seed(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	entries, err := Dictionary()
	if err != nil {
		return 0, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Собираем уникальные категории + UPSERT каждой как pseudo-жанр.
	catIDByName := map[string]int64{}
	for _, e := range entries {
		if e.Category == "" {
			continue
		}
		if _, ok := catIDByName[e.Category]; ok {
			continue
		}
		code := categoryCode(e.Category)
		id, err := upsertParent(ctx, tx, code, e.Category)
		if err != nil {
			return 0, fmt.Errorf("upsert category %q: %w", e.Category, err)
		}
		catIDByName[e.Category] = id
	}

	// 2. UPSERT каждого жанра с правильным parent_id.
	for _, e := range entries {
		var parentID *int64
		if e.Category != "" {
			if id, ok := catIDByName[e.Category]; ok {
				parentID = &id
			}
		}
		if err := upsertGenre(ctx, tx, e.Code, e.NameRu, parentID); err != nil {
			return 0, fmt.Errorf("upsert genre %q: %w", e.Code, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(entries), nil
}

// upsertParent — отдельная функция для категорий. parent_id у самой
// категории всегда NULL — это root.
func upsertParent(ctx context.Context, q pgx.Tx, code, nameRu string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO genres (fb2_code, name_ru, parent_id)
		VALUES ($1, $2, NULL)
		ON CONFLICT (fb2_code) DO UPDATE SET
		  name_ru = EXCLUDED.name_ru,
		  parent_id = NULL
		RETURNING id
	`, code, nameRu).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// upsertGenre — UPSERT леаф-жанра. ON CONFLICT перезаписывает name_ru
// и parent_id — наш словарь авторитетный. name_en НЕ трогаем (его в
// нашем источнике нет; если когда-то добавим в JSON — расширим SQL).
func upsertGenre(ctx context.Context, q pgx.Tx, code, nameRu string, parentID *int64) error {
	_, err := q.Exec(ctx, `
		INSERT INTO genres (fb2_code, name_ru, parent_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (fb2_code) DO UPDATE SET
		  name_ru = EXCLUDED.name_ru,
		  parent_id = EXCLUDED.parent_id
	`, code, nameRu, parentID)
	return err
}

// categoryCode — превращает имя категории в fb2_code для pseudo-родителя.
// Префикс "cat:" гарантирует что этот код никогда не столкнётся с
// реальным fb2-жанром (там никогда нет двоеточия).
//
// Конкретный transliteration упрощённый: lowercase + замена кириллицы
// на удобный slug. Для русских названий важна детерминированность,
// не красота — этот код используется только как FK и в SQL.
func categoryCode(name string) string {
	// Простой mapping для 22 наших категорий — не общий transliterator,
	// нам не нужно покрывать любые строки. Hand-crafted slug'и
	// соответствуют названиям категорий в genres_fb2.glst.
	switch name {
	case "Фантастика":
		return "cat:sf"
	case "Проза":
		return "cat:prose"
	case "Наука, Образование":
		return "cat:science"
	case "Детективы и Триллеры":
		return "cat:detective"
	case "Документальная литература":
		return "cat:nonfiction"
	case "Любовные романы":
		return "cat:love"
	case "Детское":
		return "cat:children"
	case "Домоводство (Дом и семья)":
		return "cat:home"
	case "Религия и духовность":
		return "cat:religion"
	case "Приключения":
		return "cat:adventure"
	case "Юмор":
		return "cat:humor"
	case "Поэзия":
		return "cat:poetry"
	case "Военное дело":
		return "cat:military"
	case "Техника":
		return "cat:tech"
	case "Справочная литература":
		return "cat:reference"
	case "Компьютеры и Интернет":
		return "cat:computers"
	case "Драматургия":
		return "cat:drama"
	case "Деловая литература":
		return "cat:business"
	case "Старинное":
		return "cat:antique"
	case "Фольклор":
		return "cat:folklore"
	case "Прочее":
		return "cat:other"
	case "Неотсортированное":
		return "cat:unsorted"
	}
	// Не должно случиться — если случится (в JSON добавили новую
	// категорию а тут забыли), вернём детерминированный fallback,
	// чтобы Seed не упал; админ увидит unfamiliar code и поправит.
	return "cat:unknown"
}
