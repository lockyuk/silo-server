package themedownload

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Sources after Plex. ThemerrDB is a community list of YouTube theme links
// keyed by TMDB id; a YouTube search is a guess an admin must approve.
const (
	SourceThemerrDB = "themerrdb"
	SourceYouTube   = "youtube"

	defaultThemerrURL = "https://app.lizardbyte.dev/ThemerrDB/%s/themoviedb/%s.json"

	// themeMaxSeconds caps a stored theme; YouTube uploads are often a whole
	// soundtrack. The last themeFadeSeconds fade out.
	themeMaxSeconds  = 150
	themeFadeSeconds = 4

	searchResults      = 8
	searchMinSeconds   = 25
	searchMaxSeconds   = 600
	ytdlpTimeout       = 3 * time.Minute
	ytdlpSearchTimeout = time.Minute
)

// errReview means a YouTube search found a likely theme that waits for an
// admin to approve it.
var errReview = errors.New("a search result waits for review")

// errNoYTDLP means a source needs yt-dlp and the server has none.
var errNoYTDLP = errors.New("yt-dlp is not installed on the server")

// Candidate is a YouTube search result waiting for an admin.
type Candidate struct {
	VideoID         string    `json:"video_id"`
	Title           string    `json:"title"`
	Channel         string    `json:"channel"`
	DurationSeconds int       `json:"duration_seconds"`
	Query           string    `json:"query"`
	FoundAt         time.Time `json:"found_at"`
}

// URL is the video's watch page, for an admin to listen before approving.
func (c Candidate) URL() string { return "https://www.youtube.com/watch?v=" + c.VideoID }

// UseYouTube enables the ThemerrDB and YouTube search sources. ytdlp and
// ffmpeg are program paths; an empty ytdlp looks for yt-dlp on PATH.
func (s *Service) UseYouTube(ytdlp, ffmpeg string) {
	if ytdlp == "" {
		if p, err := exec.LookPath("yt-dlp"); err == nil {
			ytdlp = p
		}
	}
	s.ytdlp, s.ffmpeg = ytdlp, ffmpeg
}

// themerrLookup returns the YouTube link ThemerrDB holds for a title, or
// errNotAvailable.
func (s *Service) themerrLookup(ctx context.Context, sr Series) (string, error) {
	tmdb := strings.TrimSpace(sr.TMDBID)
	if _, err := strconv.ParseUint(tmdb, 10, 64); err != nil {
		return "", errNotAvailable
	}
	kind := "tv_shows"
	if sr.Type == TypeMovie {
		kind = "movies"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(s.themerrURL, kind, tmdb), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Silo theme songs")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("asking ThemerrDB: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", errNotAvailable
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ThemerrDB returned %d", resp.StatusCode)
	}
	var entry struct {
		URL string `json:"youtube_theme_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&entry); err != nil {
		return "", fmt.Errorf("reading ThemerrDB: %w", err)
	}
	if !strings.HasPrefix(entry.URL, "https://www.youtube.com/") && !strings.HasPrefix(entry.URL, "https://youtu.be/") {
		return "", errNotAvailable
	}
	return entry.URL, nil
}

// searchQuery is what YouTube is asked for a title's theme.
func searchQuery(sr Series) string {
	if sr.Type == TypeMovie {
		q := sr.Title
		if sr.Year > 0 {
			q += " " + strconv.Itoa(sr.Year)
		}
		return q + " movie main theme"
	}
	return sr.Title + " TV series theme song opening"
}

// unlikelyTheme filters search results that are rarely the theme itself.
var unlikelyTheme = []string{"trailer", "reaction", "review", "explained", "recap", "interview", "behind the scenes", "full episode", "cover by", "piano tutorial"}

// search asks YouTube for a title's theme and returns the first plausible
// result that has not been rejected, or errNotAvailable.
func (s *Service) search(ctx context.Context, sr Series, rejected map[string]bool) (Candidate, error) {
	if s.ytdlp == "" {
		return Candidate{}, errNoYTDLP
	}
	query := searchQuery(sr)
	ctx, cancel := context.WithTimeout(ctx, ytdlpSearchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.ytdlp, "--flat-playlist", "--no-warnings", "--ignore-config",
		"--print", "%(id)s\t%(duration)s\t%(channel)s\t%(title)s",
		fmt.Sprintf("ytsearch%d:%s", searchResults, query))
	out, err := cmd.Output()
	if err != nil {
		return Candidate{}, fmt.Errorf("searching YouTube: %s", commandError(err))
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 4)
		if len(parts) != 4 || parts[0] == "" || rejected[parts[0]] {
			continue
		}
		seconds, _ := strconv.ParseFloat(parts[1], 64)
		if seconds < searchMinSeconds || seconds > searchMaxSeconds {
			continue
		}
		title := strings.ToLower(parts[3])
		if containsAny(title, unlikelyTheme) {
			continue
		}
		channel := parts[2]
		if channel == "NA" {
			channel = ""
		}
		return Candidate{VideoID: parts[0], Title: parts[3], Channel: channel, DurationSeconds: int(seconds), Query: query, FoundAt: s.now()}, nil
	}
	return Candidate{}, errNotAvailable
}

// ErrVideoUnavailable means YouTube no longer serves a video here: removed,
// private, blocked in this country or age-restricted.
var ErrVideoUnavailable = errors.New("this video is not available on YouTube")

func videoGone(msg string) bool {
	return containsAny(strings.ToLower(msg), []string{
		"not available", "unavailable", "private video", "has been removed",
		"blocked", "sign in to confirm your age", "copyright",
	})
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// youtubeAudio downloads a video's audio and returns it as an MP3 of at most
// themeMaxSeconds, fading out at the cut.
func (s *Service) youtubeAudio(ctx context.Context, url string) ([]byte, error) {
	if s.ytdlp == "" {
		return nil, errNoYTDLP
	}
	if s.ffmpeg == "" {
		return nil, errors.New("ffmpeg is not configured")
	}
	dir, err := os.MkdirTemp("", "silo-theme-yt-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ctx, cancel := context.WithTimeout(ctx, ytdlpTimeout)
	defer cancel()
	dl := exec.CommandContext(ctx, s.ytdlp, "--no-playlist", "--no-warnings", "--ignore-config", "--quiet",
		"-f", "bestaudio/best", "--max-filesize", "60M", "-o", filepath.Join(dir, "source.%(ext)s"), url)
	if out, err := dl.CombinedOutput(); err != nil {
		msg := firstLine(string(out), err)
		if videoGone(msg) {
			return nil, fmt.Errorf("%w: %s", ErrVideoUnavailable, msg)
		}
		return nil, fmt.Errorf("downloading from YouTube: %s", msg)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "source.*"))
	if len(matches) == 0 {
		return nil, errors.New("downloading from YouTube: no file was written")
	}
	target := filepath.Join(dir, "theme.mp3")
	fadeStart := themeMaxSeconds - themeFadeSeconds
	conv := exec.CommandContext(ctx, s.ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-i", matches[0],
		"-vn", "-t", strconv.Itoa(themeMaxSeconds),
		"-af", fmt.Sprintf("afade=t=out:st=%d:d=%d", fadeStart, themeFadeSeconds),
		"-ac", "2", "-ar", "44100", "-codec:a", "libmp3lame", "-b:a", "192k", target)
	if out, err := conv.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("converting theme: %s", firstLine(string(out), err))
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	if !looksLikeMP3(data) {
		return nil, errors.New("converting theme: the result is not an MP3")
	}
	return data, nil
}

func commandError(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return firstLine(string(exit.Stderr), err)
	}
	return err.Error()
}

func firstLine(out string, err error) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return err.Error()
}
