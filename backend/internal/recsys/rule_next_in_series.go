package recsys

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// NextInSeries — «Дальше в цикле» (план recsys/02, задача 2.1): для каждого
// цикла (series.kind IS NULL), где у пользователя есть прочитанная работа с
// номером N, — ближайшая по номеру следующая (N+1, с дырами в нумерации —
// ближайшая большая), если она есть в библиотеке и пользователь её ещё не
// трогал. Межавторские и издательские серии — не циклы (#448/#468); работы без
// номера порядок не задают и не предлагаются. Если следующая уже «в руках»
// (читается, приобретена, скрыта) — цикл пропускается: через неё не
// перепрыгиваем. Порядок секции — недавно прочитанные циклы сверху.
type NextInSeries struct{}

func (NextInSeries) Name() string         { return "next_in_series" }
func (NextInSeries) Section() SectionKind { return SectionNextInSeries }

// seriesAnchor — прочитанная работа цикла с наибольшим номером.
type seriesAnchor struct {
	seriesID int64
	series   string
	workID   int64
	title    string
	serNo    int
	readAt   time.Time
}

func (NextInSeries) Candidates(ctx context.Context, q Querier, p *Profile, n int) ([]Candidate, error) {
	read := p.readWorks()
	if len(read) == 0 || n <= 0 {
		return nil, nil
	}
	anchors := map[int64]*seriesAnchor{}
	if err := each(ctx, q, `
		SELECT w.id, w.series_id, s.title, w.ser_no, w.title
		FROM works w
		JOIN series s ON s.id = w.series_id AND s.kind IS NULL
		WHERE w.id = ANY($1) AND w.ser_no IS NOT NULL`,
		[]any{read}, func(rows pgx.Rows) error {
			var (
				wid, sid      int64
				series, title string
				serNo         int
			)
			if err := rows.Scan(&wid, &sid, &series, &serNo, &title); err != nil {
				return err
			}
			at := p.Signals[wid].FinishedAt()
			a := anchors[sid]
			if a == nil || serNo > a.serNo || (serNo == a.serNo && at.After(a.readAt)) {
				anchors[sid] = &seriesAnchor{seriesID: sid, series: series, workID: wid, title: title, serNo: serNo, readAt: at}
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("next in series: anchors: %w", err)
	}
	if len(anchors) == 0 {
		return nil, nil
	}
	seriesIDs := make([]int64, 0, len(anchors))
	for sid := range anchors {
		seriesIDs = append(seriesIDs, sid)
	}

	// Нумерованные работы этих циклов с живым изданием, по номеру.
	type member struct {
		workID int64
		serNo  int
	}
	members := map[int64][]member{}
	if err := each(ctx, q, `
		SELECT w.id, w.series_id, w.ser_no
		FROM works w
		WHERE w.series_id = ANY($1) AND w.ser_no IS NOT NULL
		  AND NOT w.instance_hidden AND COALESCE(w.kind, '') = ''
		  AND EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id AND NOT b.deleted AND NOT b.instance_hidden)
		ORDER BY w.series_id, w.ser_no, w.id`,
		[]any{seriesIDs}, func(rows pgx.Rows) error {
			var (
				wid, sid int64
				serNo    int
			)
			if err := rows.Scan(&wid, &sid, &serNo); err != nil {
				return err
			}
			members[sid] = append(members[sid], member{workID: wid, serNo: serNo})
			return nil
		}); err != nil {
		return nil, fmt.Errorf("next in series: members: %w", err)
	}

	out := make([]Candidate, 0, len(anchors))
	for sid, a := range anchors {
		// Ближайший больший номер; несколько работ под одним номером (два
		// перевода как разные работы) — любая «в руках» блокирует цикл.
		next, pick, blocked := 0, int64(0), false
		for _, m := range members[sid] {
			if m.serNo <= a.serNo {
				continue
			}
			if next == 0 {
				next = m.serNo
			}
			if m.serNo != next {
				break
			}
			if p.Engaged(m.workID) {
				blocked = true
				break
			}
			if pick == 0 {
				pick = m.workID
			}
		}
		if pick == 0 || blocked {
			continue
		}
		out = append(out, Candidate{
			WorkID: pick,
			Score:  float64(a.readAt.UnixMilli()), // недавно прочитанные циклы — выше
			Reason: Reason{
				Kind: ReasonNextInSeries, SeriesID: sid, Series: a.series,
				WorkID: a.workID, Title: a.title, SerNo: next, At: timePtr(a.readAt),
			},
		})
	}
	sortCandidates(out)
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}
