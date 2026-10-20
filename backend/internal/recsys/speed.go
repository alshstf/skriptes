package recsys

import (
	"math"
	"sort"
	"time"
)

// Личная скорость чтения (план recsys/01, задача 1.2) — из журнала позиций
// read_positions. Единица — байты fb2 в минуту: длина текста пока = размер
// файла (размер и скорость в одних единицах, оценка «времени дочитать» от
// единицы не зависит). Книги больше MaxSpeedFileBytes (иллюстрации) не в счёт.

const (
	// MaxSpeedFileBytes — файлы крупнее считаем «с картинками», в скорость не берём.
	MaxSpeedFileBytes = 3 << 20
	// SessionGap — разрыв между позициями, после которого начинается новая сессия.
	SessionGap = 30 * time.Minute
	// MinSessionDuration — короче — шум (открыл и закрыл).
	MinSessionDuration = 2 * time.Minute
	// SpeedSessions — медиана по стольким последним сессиям.
	SpeedSessions = 20
)

// PositionSample — строка журнала позиций с размером книги.
type PositionSample struct {
	BookID    int64
	Fraction  float64
	At        time.Time
	SizeBytes int64
}

// Speed — оценка скорости: байт в минуту и по скольким сессиям.
type Speed struct {
	BytesPerMin float64
	Sessions    int
}

// Known — скорость есть (достаточно данных).
func (s Speed) Known() bool { return s.BytesPerMin > 0 && s.Sessions > 0 }

// MinutesFor — сколько минут на bytes при этой скорости (0 — неизвестно).
func (s Speed) MinutesFor(bytes int64) int {
	if !s.Known() || bytes <= 0 {
		return 0
	}
	return int(math.Round(float64(bytes) / s.BytesPerMin))
}

// EstimateSpeed — сессии = цепочки соседних позиций одной книги с разрывом
// < SessionGap; скорость сессии = прочитанные байты / минуты; итог — медиана
// по последним SpeedSessions сессиям (устойчива к одной «пролистанной» книге).
// samples — в любом порядке.
func EstimateSpeed(samples []PositionSample) Speed {
	byBook := map[int64][]PositionSample{}
	for _, s := range samples {
		if s.SizeBytes <= 0 || s.SizeBytes > MaxSpeedFileBytes {
			continue
		}
		byBook[s.BookID] = append(byBook[s.BookID], s)
	}
	type session struct {
		end   time.Time
		speed float64
	}
	var sessions []session
	for _, ps := range byBook {
		sort.Slice(ps, func(i, j int) bool { return ps[i].At.Before(ps[j].At) })
		var (
			bytes float64
			dur   time.Duration
		)
		flush := func(end time.Time) {
			if dur >= MinSessionDuration && bytes > 0 {
				sessions = append(sessions, session{end: end, speed: bytes / dur.Minutes()})
			}
			bytes, dur = 0, 0
		}
		for i := 1; i < len(ps); i++ {
			gap := ps[i].At.Sub(ps[i-1].At)
			if gap >= SessionGap || gap <= 0 {
				flush(ps[i-1].At)
				continue
			}
			// Назад (перелистал к началу) — не чтение; вперёд — прочитанные байты.
			if df := ps[i].Fraction - ps[i-1].Fraction; df > 0 {
				bytes += df * float64(ps[i].SizeBytes)
			}
			dur += gap
		}
		flush(ps[len(ps)-1].At)
	}
	if len(sessions) == 0 {
		return Speed{}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].end.After(sessions[j].end) })
	if len(sessions) > SpeedSessions {
		sessions = sessions[:SpeedSessions]
	}
	speeds := make([]float64, len(sessions))
	for i, s := range sessions {
		speeds[i] = s.speed
	}
	sort.Float64s(speeds)
	n := len(speeds)
	median := speeds[n/2]
	if n%2 == 0 {
		median = (speeds[n/2-1] + speeds[n/2]) / 2
	}
	return Speed{BytesPerMin: median, Sessions: n}
}
