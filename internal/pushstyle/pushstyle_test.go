package pushstyle

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDefaultsRenderBuiltInText(t *testing.T) {
	s := DefaultSettings()
	got, err := Render(s, Sample(KindEpisode))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Severance" || got.Body != "New episode · S02E01 · Hello, Ms. Cobel" || got.Synopsis == "" {
		t.Fatalf("episode = %+v", got)
	}
	got, _ = Render(s, Sample(KindMovie))
	if got.Title != "Dune: Part Two (2024)" || got.Body != "New movie" {
		t.Fatalf("movie = %+v", got)
	}
	got, _ = Render(s, Sample(KindGroup))
	if got.Body != "3 new episodes · S02E05–E07" || got.Synopsis != "" {
		t.Fatalf("group = %+v", got)
	}
}

func TestRenderEscapesValuesKeepsFormattingAndDropsNewlines(t *testing.T) {
	s := DefaultSettings()
	s.EpisodeTitle = "<b>{{series}}</b> &\n{{code}}"
	got, err := Render(s, Item{Kind: KindEpisode, Title: "Law & <Order>", Episodes: []Episode{{}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "<b>Law &amp; &lt;Order&gt;</b> &" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestNormalizeRejectsBadFields(t *testing.T) {
	for field, mutate := range map[string]func(*Settings){
		"episode_body":  func(s *Settings) { s.EpisodeBody = "{{#open}}" },
		"image":         func(s *Settings) { s.Image = "huge" },
		"quiet_start":   func(s *Settings) { s.QuietStart = "25:00" },
		"timezone":      func(s *Settings) { s.Timezone = "Mars/Olympus" },
		"batch_minutes": func(s *Settings) { s.BatchMinutes = -1 },
	} {
		s := DefaultSettings()
		mutate(&s)
		_, err := s.Normalize()
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("%s: err = %v", field, err)
		}
	}
	s, err := Settings{}.Normalize()
	if err != nil || s.EpisodeTitle != DefaultSettings().EpisodeTitle || s.Image != ImagePoster {
		t.Fatalf("blank settings = %+v, %v", s, err)
	}
}

func TestQuietUntil(t *testing.T) {
	s := DefaultSettings()
	s.QuietEnabled, s.Timezone = true, "Europe/London"
	loc, _ := time.LoadLocation("Europe/London")
	at := func(h, m int) time.Time { return time.Date(2026, 10, 27, h, m, 0, 0, loc) }

	if until, ok := s.QuietUntil(at(23, 30)); !ok || !until.Equal(time.Date(2026, 10, 28, 7, 0, 0, 0, loc)) {
		t.Fatalf("23:30 -> %v %v", until, ok)
	}
	if until, ok := s.QuietUntil(at(3, 0)); !ok || !until.Equal(at(7, 0)) {
		t.Fatalf("03:00 -> %v %v", until, ok)
	}
	if _, ok := s.QuietUntil(at(12, 0)); ok {
		t.Fatal("noon is not quiet")
	}
	s.QuietStart, s.QuietEnd = "13:00", "15:00"
	if until, ok := s.QuietUntil(at(14, 0)); !ok || !until.Equal(at(15, 0)) {
		t.Fatalf("daytime window -> %v %v", until, ok)
	}
	s.QuietEnabled = false
	if _, ok := s.QuietUntil(at(14, 0)); ok {
		t.Fatal("off means never quiet")
	}
}

func TestTruncate(t *testing.T) {
	long := strings.Repeat("word ", 40)
	got := Truncate(long, 30)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > 31 || strings.Contains(got, "wo…") {
		t.Fatalf("truncate = %q", got)
	}
	if Truncate("short", 30) != "short" {
		t.Fatal("short text changed")
	}
}

func TestJoinCodes(t *testing.T) {
	for in, want := range map[string]string{
		"S01E01":               "S01E01",
		"S01E01,S01E02":        "S01E01, S01E02",
		"S01E01,S01E02,S01E05": "S01E01–E05",
		"S01E09,S01E10,S02E01": "S01E09 … S02E01",
	} {
		if got := joinCodes(strings.Split(in, ",")); got != want {
			t.Errorf("%s = %q, want %q", in, got, want)
		}
	}
}
