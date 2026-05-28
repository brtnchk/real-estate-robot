package fetcher

import (
	"testing"
	"time"
)

func TestRecentlyFetched(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		fetchedAt time.Time
		interval  time.Duration
		want      bool
	}{
		{
			name:      "interval=0 disables dedup",
			fetchedAt: now.Add(-time.Second),
			interval:  0,
			want:      false,
		},
		{
			name:      "zero fetchedAt means never seen",
			fetchedAt: time.Time{},
			interval:  time.Hour,
			want:      false,
		},
		{
			name:      "fetched 1m ago, window 5m -> recent",
			fetchedAt: now.Add(-1 * time.Minute),
			interval:  5 * time.Minute,
			want:      true,
		},
		{
			name:      "fetched 5m ago exactly, window 5m -> NOT recent (boundary)",
			fetchedAt: now.Add(-5 * time.Minute),
			interval:  5 * time.Minute,
			want:      false,
		},
		{
			name:      "fetched 10m ago, window 5m -> not recent",
			fetchedAt: now.Add(-10 * time.Minute),
			interval:  5 * time.Minute,
			want:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recentlyFetched(tc.fetchedAt, now, tc.interval); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}