package catalog

import (
	"context"
	"sort"
)

// EraSplit — подсказка администратору, что под одним именем, похоже, два разных
// автора (#356): работы распадаются на две эпохи с большим разрывом — «Берг
// Николай» — поэт 1850–1873 и автор сетевой литературы 2010–2025. INPX различает
// тёзок только уточнением в фамилии, а здесь его нет. Решает администратор
// (разделение — POST /api/admin/authors/{id}/split), автоматически ничего не
// делим.
type EraSplit struct {
	Older EraGroup `json:"older"`
	Newer EraGroup `json:"newer"`
}

// EraGroup — работы одной эпохи.
type EraGroup struct {
	From    int     `json:"from"`
	To      int     `json:"to"`
	WorkIDs []int64 `json:"work_ids"`
}

// eraSplitMinGap — разрыв в годах написания, после которого это уже не одна жизнь.
const eraSplitMinGap = 80

type workYear struct {
	workID int64
	year   int
}

// detectEraSplit — самый большой разрыв между соседними годами написания работ;
// подсказка, если он не меньше eraSplitMinGap и по обе стороны хотя бы две работы
// и десятая часть датированных (единичные ошибки дат у классика не в счёт).
func detectEraSplit(works []workYear) *EraSplit {
	var dated []workYear
	for _, w := range works {
		if w.year >= 1000 && w.year <= 2100 {
			dated = append(dated, w)
		}
	}
	if len(dated) < 4 {
		return nil
	}
	sort.SliceStable(dated, func(i, j int) bool { return dated[i].year < dated[j].year })
	cut, gap := -1, 0
	for i := 1; i < len(dated); i++ {
		if d := dated[i].year - dated[i-1].year; d > gap {
			cut, gap = i, d
		}
	}
	if gap < eraSplitMinGap {
		return nil
	}
	older, newer := dated[:cut], dated[cut:]
	minSide := (len(dated) + 9) / 10 // десятая часть, с округлением вверх
	if minSide < 2 {
		minSide = 2
	}
	if len(older) < minSide || len(newer) < minSide {
		return nil
	}
	group := func(ws []workYear) EraGroup {
		g := EraGroup{From: ws[0].year, To: ws[len(ws)-1].year}
		for _, w := range ws {
			g.WorkIDs = append(g.WorkIDs, w.workID)
		}
		return g
	}
	return &EraSplit{Older: group(older), Newer: group(newer)}
}

// queryEraSplit — годы написания работ автора (без учёта скрытого: подсказка для
// администратора) → detectEraSplit.
func (s *Service) queryEraSplit(ctx context.Context, authorID int64) (*EraSplit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(b.work_id, -b.id) AS w,
		       min(COALESCE(w.written_year, b.written_year))::int AS y
		FROM book_authors ba
		JOIN books b      ON b.id = ba.book_id AND b.deleted = false
		LEFT JOIN works w ON w.id = b.work_id
		WHERE ba.author_id = $1
		GROUP BY 1`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ws []workYear
	for rows.Next() {
		var w workYear
		var y *int
		if err := rows.Scan(&w.workID, &y); err != nil {
			return nil, err
		}
		if y != nil {
			w.year = *y
		}
		ws = append(ws, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return detectEraSplit(ws), nil
}
