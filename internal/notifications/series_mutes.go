package notifications

import "context"

// FollowSeries makes the profile follow the series: its new episodes notify
// the profile whether or not it asked for everything new. Following a series
// already followed changes nothing, and lifts any mute of it. It returns
// ErrSeriesNotFound when the catalog has no series with that ID; catalog
// access is the caller's check.
func (s *System) FollowSeries(ctx context.Context, userID int, profileID, seriesID string) error {
	return s.Follows.follow(ctx, userID, profileID, seriesID)
}

// UnfollowSeries stops the profile following the series. Unfollowing a series
// that is not followed changes nothing.
func (s *System) UnfollowSeries(ctx context.Context, userID int, profileID, seriesID string) error {
	return s.Follows.unfollow(ctx, userID, profileID, seriesID)
}

// MuteSeries mutes the series for the profile: a profile that asked for
// everything new hears nothing about it until it unmutes. Muting a series
// already muted changes nothing. It returns ErrSeriesNotFound when the
// catalog has no series with that ID; catalog access is the caller's check.
func (s *System) MuteSeries(ctx context.Context, userID int, profileID, seriesID string) error {
	return s.Follows.muteSeries(ctx, userID, profileID, seriesID)
}

// UnmuteSeries lifts the profile's mute of the series. Unmuting a series that
// is not muted changes nothing. Fanout reads the mute when it evaluates a
// release, so an episode it evaluated while the series was muted stays
// unannounced, and one still waiting for fanout at unmute is announced.
func (s *System) UnmuteSeries(ctx context.Context, userID int, profileID, seriesID string) error {
	return s.Follows.unmuteSeries(ctx, userID, profileID, seriesID)
}
