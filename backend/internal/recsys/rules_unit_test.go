package recsys

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEngagedAndRead — что считается прочитанным и «в руках» (без БД).
func TestEngagedAndRead(t *testing.T) {
	now := time.Now()
	completed := &WorkSignals{WorkID: 1, Completed: true, CompletedAt: now.Add(-time.Hour)}
	rated := &WorkSignals{WorkID: 2, Rating: 4, RatedAt: now}
	viewed := &WorkSignals{WorkID: 3, Views: 2, ViewedAt: now}
	shelved := &WorkSignals{WorkID: 4, Favorite: true, OnShelf: true}
	opened := &WorkSignals{WorkID: 5, Opened: true}
	acquired := &WorkSignals{WorkID: 6, Acquired: true}
	dismissed := &WorkSignals{WorkID: 7, Dismissed: true, DismissReason: DismissLater}
	never := &WorkSignals{WorkID: 8, PromptNever: true}

	require.True(t, completed.Read())
	require.True(t, rated.Read())
	require.False(t, viewed.Read())
	require.Equal(t, now, rated.FinishedAt())
	require.Equal(t, now.Add(-time.Hour), completed.FinishedAt())
	both := &WorkSignals{Completed: true, CompletedAt: now.Add(-time.Hour), Rating: 5, RatedAt: now}
	require.Equal(t, now, both.FinishedAt(), "позднее из дочитано/оценено")
	var nilSig *WorkSignals
	require.False(t, nilSig.Read())
	require.True(t, nilSig.FinishedAt().IsZero())

	p := &Profile{Signals: map[int64]*WorkSignals{}}
	for _, s := range []*WorkSignals{completed, rated, viewed, shelved, opened, acquired, dismissed, never} {
		p.Signals[s.WorkID] = s
	}
	for id, want := range map[int64]bool{1: true, 2: true, 3: false, 4: false, 5: true, 6: true, 7: true, 8: true, 9: false} {
		require.Equal(t, want, p.Engaged(id), "work %d", id)
	}
	require.Equal(t, []int64{1, 2}, p.readWorks())
	var nilProfile *Profile
	require.False(t, nilProfile.Engaged(1))
	require.Nil(t, nilProfile.readWorks())
}

// TestAuthorLiked — пороги «автор понравился».
func TestAuthorLiked(t *testing.T) {
	require.False(t, (&authorAnchor{}).liked())
	require.False(t, (&authorAnchor{read: 1}).liked(), "одно прочитанное без оценок — мало")
	require.True(t, (&authorAnchor{read: AuthorMinReadUnrated}).liked())
	require.True(t, (&authorAnchor{read: 1, rating: AuthorRating{Count: 1, Sum: 4}}).liked())
	require.False(t, (&authorAnchor{read: 3, rating: AuthorRating{Count: 2, Sum: 7}}).liked(), "средняя 3.5 < 4")
	require.True(t, (&authorAnchor{read: 2, rating: AuthorRating{Count: 2, Sum: 9}}).liked())
}

// TestAuthorRating — «ваша средняя» (recsys/03, 3.6): без оценок — не известна, 0 не считается.
func TestAuthorRating(t *testing.T) {
	var r AuthorRating
	require.False(t, r.Known())
	require.Equal(t, 0.0, r.Avg())
	r.Add(0)
	require.False(t, r.Known(), "0 — нет оценки")
	r.Add(5)
	r.Add(4)
	require.True(t, r.Known())
	require.Equal(t, 2, r.Count)
	require.InDelta(t, 4.5, r.Avg(), 1e-9)
}

// TestSortCandidates — порядок: по весу, при равенстве — по id.
func TestSortCandidates(t *testing.T) {
	c := []Candidate{{WorkID: 3, Score: 1}, {WorkID: 1, Score: 2}, {WorkID: 2, Score: 1}}
	sortCandidates(c)
	require.Equal(t, []int64{1, 2, 3}, []int64{c[0].WorkID, c[1].WorkID, c[2].WorkID})
}

// TestIsSectionKind — закрытый список секций.
func TestIsSectionKind(t *testing.T) {
	for _, k := range AllSections {
		require.True(t, IsSectionKind(string(k)))
	}
	require.False(t, IsSectionKind("nope"))
	require.False(t, IsSectionKind(""))
	for _, r := range Rules {
		require.True(t, IsSectionKind(string(r.Section())), "секция правила %s должна быть в AllSections", r.Name())
		_, ok := RuleFor(r.Section())
		require.True(t, ok)
	}
	_, ok := RuleFor(SectionContinueReading)
	require.False(t, ok)
}
