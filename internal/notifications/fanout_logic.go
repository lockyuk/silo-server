package notifications

import (
	"sort"
)

// EvaluateRecipient applies the eligibility rules for one candidate recipient
// of one release event. Returns the matched reason flags and whether a
// delivery should be created.
//
//   - a profile that follows the series is notified of any newly available
//     episode of it
//   - a profile that asked for everything new is notified of any newly
//     available episode, unless it muted the series (the candidate query
//     leaves those out)
//   - a follower that did not ask for everything new is not told again of an
//     episode at or below the last it was told about
//   - a disabled profile is never notified
func EvaluateRecipient(candidate FanoutCandidate, prefs Preferences, episodeKey int) (ReasonFlags, bool) {
	if !prefs.Enabled {
		return ReasonFlags{}, false
	}
	// A profile that asked for every new episode hears about any episode
	// newly added, including one older than the last it was told about (a
	// back-filled season); the per-episode unique key still stops repeats.
	if !prefs.NotifyNewEpisodes && candidate.LastNotifiedEpisodeKey != nil && *candidate.LastNotifiedEpisodeKey >= episodeKey {
		return ReasonFlags{}, false
	}
	flags := ReasonFlags{Followed: candidate.Followed, NewRelease: prefs.NotifyNewEpisodes}
	return flags, flags.Any()
}

// EvaluateMovieRecipient applies the eligibility rule for a new movie: the
// profile is enabled and asked for every new movie.
func EvaluateMovieRecipient(prefs Preferences) (ReasonFlags, bool) {
	if !prefs.Enabled || !prefs.NotifyNewMovies {
		return ReasonFlags{}, false
	}
	return ReasonFlags{NewRelease: true}, true
}

// PartitionEventsByKind splits claimed events into episode events (which fan
// out to interested profiles) and everything else (flat item kinds — movies,
// audiobooks, ebooks). Movies fan out to profiles that asked for every new
// movie; the other flat kinds only feed the server-channel broadcast sweep.
// Order is preserved within each partition. The common all-episode batch
// returns the input slice unchanged.
func PartitionEventsByKind(events []ReleaseEvent) (episodes, others []ReleaseEvent) {
	allEpisodes := true
	for _, event := range events {
		if normalizeEventKind(event.Kind) != EventKindEpisode {
			allEpisodes = false
			break
		}
	}
	if allEpisodes {
		return events, nil
	}
	episodes = make([]ReleaseEvent, 0, len(events))
	others = make([]ReleaseEvent, 0)
	for _, event := range events {
		if normalizeEventKind(event.Kind) == EventKindEpisode {
			episodes = append(episodes, event)
		} else {
			others = append(others, event)
		}
	}
	return episodes, others
}

// ApplyBurstCap groups claimed events by (library, series) and bounds fanout
// per group: only the maxPerSeries events with the highest episode_key fan
// out; the rest are suppressed (processed without deliveries). This bounds
// the blast radius of bulk additions (back-catalog season packs) to seeded
// libraries. The cap is per claim batch and therefore approximate across
// batches, which is acceptable.
func ApplyBurstCap(events []ReleaseEvent, maxPerSeries int) (fanout, suppressed []ReleaseEvent) {
	if maxPerSeries <= 0 {
		maxPerSeries = 1
	}
	type groupKey struct {
		libraryID int
		seriesID  string
	}
	groups := make(map[groupKey][]ReleaseEvent)
	order := make([]groupKey, 0)
	for _, event := range events {
		key := groupKey{event.LibraryID, event.SeriesID}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], event)
	}

	fanout = make([]ReleaseEvent, 0, len(events))
	suppressed = make([]ReleaseEvent, 0)
	for _, key := range order {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool {
			return group[i].EpisodeKey > group[j].EpisodeKey
		})
		keep := min(maxPerSeries, len(group))
		suppressed = append(suppressed, group[keep:]...)
		// Emit the kept events in ascending key order: fanout raises
		// last_notified_episode_key as it processes each event, so a higher
		// key processed first would make EvaluateRecipient suppress every
		// remaining lower-key event in the group.
		for i := keep - 1; i >= 0; i-- {
			fanout = append(fanout, group[i])
		}
	}
	return fanout, suppressed
}
