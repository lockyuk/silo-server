package filmography

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/metadata"
)

func TestIsSelf(t *testing.T) {
	for character, want := range map[string]bool{
		"Self": true, "Himself": true, "Self - Guest": true, "Herself (archive footage)": true, "self, host": true,
		"Tyler Durden": false, "Selfish Man": false, "Himselfish": false, "": false,
	} {
		if got := isSelf(character); got != want {
			t.Errorf("%q: got %v want %v", character, got, want)
		}
	}
}

func TestGroupMergesRolesAndSortsNewestFirst(t *testing.T) {
	ids := func(id string) map[string]string { return map[string]string{"tmdb": id} }
	got := group([]metadata.PersonCredit{
		{Provider: "tmdb", ItemType: "movie", ProviderIDs: ids("1"), Title: "Old", ReleaseDate: "1999-01-01", Department: "Acting", Character: "A"},
		{Provider: "tmdb", ItemType: "movie", ProviderIDs: ids("2"), Title: "New", ReleaseDate: "2020-05-01", Department: "Acting", Character: "B"},
		{Provider: "tmdb", ItemType: "movie", ProviderIDs: ids("2"), Title: "New", ReleaseDate: "2020-05-01", Department: "Directing", Job: "Director"},
		{Provider: "tmdb", ItemType: "series", ProviderIDs: ids("2"), Title: "Same id, a show", ReleaseDate: "2010-01-01", Department: "Acting", Character: "Self", EpisodeCount: 3},
		{Provider: "tmdb", ItemType: "movie", ProviderIDs: ids("3"), Title: "Upcoming"},
		{Provider: "tmdb", ItemType: "person", ProviderIDs: ids("4"), Title: "skipped"},
		{Provider: "tmdb", ItemType: "movie", ProviderIDs: map[string]string{}, Title: "no id"},
	})
	var titles []string
	for _, c := range got {
		titles = append(titles, c.Title)
	}
	if strings.Join(titles, ",") != "Upcoming,New,Same id, a show,Old" {
		t.Fatalf("order: %v", titles)
	}
	if len(got[1].Roles) != 2 || got[1].Year != 2020 || got[1].Roles[1].Job != "Director" {
		t.Fatalf("merged: %+v", got[1])
	}
	if !got[2].Roles[0].Self || got[2].ItemType != "series" {
		t.Fatalf("series self credit: %+v", got[2])
	}
}

type fakeProvider struct {
	slug    string
	credits []metadata.PersonCredit
	err     error
	calls   int
}

func (f *fakeProvider) Slug() string       { return f.slug }
func (f *fakeProvider) Name() string       { return f.slug }
func (f *fakeProvider) ForTypes() []string { return []string{"movie", "series"} }
func (f *fakeProvider) GetPersonCredits(_ context.Context, req metadata.PersonDetailRequest) ([]metadata.PersonCredit, error) {
	f.calls++
	if req.ProviderIDs["tmdb"] == "" {
		return nil, errors.New("no tmdb id sent")
	}
	return f.credits, f.err
}

type unsupported struct{ fakeProvider }

func (u *unsupported) GetPersonCredits(context.Context, metadata.PersonDetailRequest) ([]metadata.PersonCredit, error) {
	u.calls++
	return nil, metadata.ErrPersonCreditsUnsupported
}

type fakeImages struct{}

func (fakeImages) ResolveImageURLs(_ context.Context, paths []string, variant string) map[string]string {
	out := map[string]string{}
	for _, p := range paths {
		out[p] = "https://img.test/" + variant + "/" + strings.TrimPrefix(p, "tmdb://")
	}
	return out
}

type memSettings map[string]string

func (m memSettings) Get(_ context.Context, key string) (string, error) { return m[key], nil }
func (m memSettings) Set(_ context.Context, key, value string) error    { m[key] = value; return nil }

func TestFilmographyMatchesLibraryAndCachesPostgres(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	prefix := fmt.Sprintf("filmo-%d", time.Now().UnixNano())
	var shared, hidden int
	for name, dst := range map[string]*int{prefix + "-shared": &shared, prefix + "-hidden": &hidden} {
		if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies',$1,true) RETURNING id`, name).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	person := time.Now().UnixNano()
	if _, err := pool.Exec(ctx, `INSERT INTO people(id, name, tmdb_id) VALUES($1, $2, '287')`, person, prefix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM people WHERE id=$1`, person)
		_, _ = pool.Exec(c, `DELETE FROM media_items WHERE content_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(c, `DELETE FROM media_folders WHERE id = ANY($1)`, []int{shared, hidden})
	})
	// TMDB ids are unique per type in the test database only within this
	// test, so they carry the timestamp.
	tm := func(n int) string { return fmt.Sprintf("%d%d", time.Now().UnixNano()%1_000_000_000, n) }
	movieID, showID, hiddenID, otherTypeID := tm(1), tm(2), tm(3), tm(4)
	for _, item := range []struct {
		id, kind, tmdb string
		folder         int
	}{
		{prefix + "-movie", "movie", movieID, shared},
		{prefix + "-show", "series", showID, shared},
		{prefix + "-hidden", "movie", hiddenID, hidden},
		// A series sharing a movie credit's TMDB number is a different title.
		{prefix + "-clash", "series", otherTypeID, shared},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title,status,tmdb_id) VALUES($1,$2,$1,'released',$3)`, item.id, item.kind, item.tmdb); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, item.id, item.folder); err != nil {
			t.Fatal(err)
		}
	}
	credit := func(kind, id, title, date string) metadata.PersonCredit {
		return metadata.PersonCredit{Provider: "tmdb", ItemType: kind, ProviderIDs: map[string]string{"tmdb": id}, Title: title, ReleaseDate: date,
			Department: "Acting", Character: "Role", PosterPath: "tmdb://poster/" + id + ".jpg"}
	}
	provider := &fakeProvider{slug: "tmdb", credits: []metadata.PersonCredit{
		credit("movie", movieID, "Owned Movie", "2001-01-01"),
		credit("series", showID, "Owned Show", "2005-01-01"),
		credit("movie", hiddenID, "Hidden Library Movie", "2003-01-01"),
		credit("movie", otherTypeID, "Missing Movie", "2010-01-01"),
	}}
	none := &unsupported{fakeProvider{slug: "tvdb"}}
	settings := memSettings{}
	s := New(pool, func(context.Context) ([]metadata.Provider, error) { return []metadata.Provider{none, provider}, nil },
		fakeImages{}, catalog.NewItemRepository(pool), settings)
	if _, err := s.SetRequestURL(ctx, "http://seerr.test:5055/"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRequestURL(ctx, "seerr.test"); !errors.Is(err, ErrInvalidRequestURL) {
		t.Fatalf("bare host accepted: %v", err)
	}

	viewer := catalog.AccessFilter{AllowedLibraryIDs: []int{shared}}
	f, err := s.Get(ctx, person, viewer)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Available || len(f.Credits) != 4 || none.calls != 1 || provider.calls != 1 {
		t.Fatalf("filmography: %+v (unsupported calls %d, provider calls %d)", f, none.calls, provider.calls)
	}
	byTitle := map[string]Credit{}
	for _, c := range f.Credits {
		byTitle[c.Title] = c
	}
	if byTitle["Owned Movie"].ItemID != prefix+"-movie" || byTitle["Owned Show"].ItemID != prefix+"-show" {
		t.Fatalf("library matches: %+v", byTitle)
	}
	if byTitle["Hidden Library Movie"].ItemID != "" {
		t.Fatal("a title in a library the viewer cannot open is linked")
	}
	if byTitle["Missing Movie"].ItemID != "" || byTitle["Missing Movie"].RequestURL != "http://seerr.test:5055/movie/"+otherTypeID {
		t.Fatalf("a movie matched a series with the same TMDB number, or lacks its request link: %+v", byTitle["Missing Movie"])
	}
	if byTitle["Owned Movie"].RequestURL != "" || byTitle["Owned Movie"].PosterURL != "https://img.test/card/poster/"+movieID+".jpg" {
		t.Fatalf("owned movie: %+v", byTitle["Owned Movie"])
	}
	if byTitle["Hidden Library Movie"].RequestURL == "" {
		t.Fatal("a title the viewer cannot open has no request link")
	}

	// A second view reuses the cache; a stale cache survives a provider error.
	if _, err := s.Get(ctx, person, viewer); err != nil || provider.calls != 1 {
		t.Fatalf("cached read: calls=%d err=%v", provider.calls, err)
	}
	s.now = func() time.Time { return time.Now().Add(cacheTTL + time.Hour) }
	provider.err = errors.New("provider down")
	f, err = s.Get(ctx, person, viewer)
	if err != nil || provider.calls != 2 || len(f.Credits) != 4 {
		t.Fatalf("stale fallback: calls=%d credits=%d err=%v", provider.calls, len(f.Credits), err)
	}
	// The failure is not retried on every view.
	if _, err := s.Get(ctx, person, viewer); err != nil || provider.calls != 2 {
		t.Fatalf("failure backoff: calls=%d err=%v", provider.calls, err)
	}

	if _, err := s.Get(ctx, -1, viewer); !errors.Is(err, ErrPersonNotFound) {
		t.Fatalf("unknown person: %v", err)
	}
}
