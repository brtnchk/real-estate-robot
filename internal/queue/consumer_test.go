package queue

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

// TestXDeathCount covers the meanings we care about:
//   - no header / empty → 0
//   - "rejected" entries are counted
//   - "expired" entries (the TTL hop on the retry queue) are NOT counted
//   - count can arrive as int / int32 / int64 depending on broker/client
//   - malformed entries are silently skipped
func TestXDeathCount(t *testing.T) {
	cases := []struct {
		name    string
		headers amqp.Table
		want    int
	}{
		{
			name:    "nil headers",
			headers: nil,
			want:    0,
		},
		{
			name:    "no x-death key",
			headers: amqp.Table{"other": "thing"},
			want:    0,
		},
		{
			name: "single rejection",
			headers: amqp.Table{
				"x-death": []interface{}{
					amqp.Table{"queue": "listings.fetch", "reason": "rejected", "count": int64(1)},
				},
			},
			want: 1,
		},
		{
			name: "rejection plus expired hop (one retry round)",
			headers: amqp.Table{
				"x-death": []interface{}{
					amqp.Table{"queue": "listings.fetch", "reason": "rejected", "count": int64(1)},
					amqp.Table{"queue": "listings.fetch.retry", "reason": "expired", "count": int64(1)},
				},
			},
			want: 1, // expired is ignored
		},
		{
			name: "three rejections accumulated",
			headers: amqp.Table{
				"x-death": []interface{}{
					amqp.Table{"queue": "listings.fetch", "reason": "rejected", "count": int64(3)},
					amqp.Table{"queue": "listings.fetch.retry", "reason": "expired", "count": int64(3)},
				},
			},
			want: 3,
		},
		{
			name: "count as int32 (typical AMQP wire encoding)",
			headers: amqp.Table{
				"x-death": []interface{}{
					amqp.Table{"reason": "rejected", "count": int32(2)},
				},
			},
			want: 2,
		},
		{
			name: "count as native int",
			headers: amqp.Table{
				"x-death": []interface{}{
					amqp.Table{"reason": "rejected", "count": 5},
				},
			},
			want: 5,
		},
		{
			name: "malformed entries are skipped, not panicked over",
			headers: amqp.Table{
				"x-death": []interface{}{
					"garbage-string-entry",
					amqp.Table{"reason": "rejected", "count": int64(7)},
					42, // not a table
				},
			},
			want: 7,
		},
		{
			name: "x-death is not a slice",
			headers: amqp.Table{
				"x-death": "not-a-slice",
			},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := xDeathCount(tc.headers)
			if got != tc.want {
				t.Errorf("xDeathCount: got %d, want %d", got, tc.want)
			}
		})
	}
}