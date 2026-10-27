package notifications

import "testing"

func TestPayloadForRowOverview(t *testing.T) {
	episodeID := "episode-tvdb-304262-6-13"
	cases := []struct {
		name string
		row  DeliveryRow
		want string
	}{
		{
			name: "episode alert shows the episode synopsis",
			row:  DeliveryRow{Delivery: Delivery{Type: "episode.available", EpisodeID: &episodeID}, SeriesOverview: "Series overview.", EpisodeOverview: "Episode overview."},
			want: "Episode overview.",
		},
		{
			name: "episode without a synopsis stays empty",
			row:  DeliveryRow{Delivery: Delivery{Type: "episode.available", EpisodeID: &episodeID}, SeriesOverview: "Series overview."},
			want: "",
		},
		{
			name: "movie alert shows the movie synopsis",
			row:  DeliveryRow{Delivery: Delivery{Type: "movie.available"}, SeriesOverview: "Movie overview."},
			want: "Movie overview.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PayloadForRow(tc.row).Overview; got != tc.want {
				t.Fatalf("Overview = %q, want %q", got, tc.want)
			}
		})
	}
}
