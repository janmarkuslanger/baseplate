package tests

import (
	"regexp"
	"testing"
	"time"
)

var updatedRe = regexp.MustCompile(`<td class="muted" title="([^"]+)">`)

// TestAdminDatesUseSiteTimezone checks that server-rendered dates are
// formatted in the site's time zone, not the server's.
func TestAdminDatesUseSiteTimezone(t *testing.T) {
	// Pick a zone whose offset differs from the server's by many hours.
	zone := "Pacific/Kiritimati" // UTC+14
	if _, off := time.Now().Zone(); off > 0 {
		zone = "Pacific/Pago_Pago" // UTC-11
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Skip("time zone database unavailable:", err)
	}
	_, c := newTestApp(t, blogPlugin{})
	c.login()
	c.save("settings", "published", map[string]any{"siteName": "A", "baseURL": "https://a.test", "timezone": zone})
	c.save("post", "published", map[string]any{"title": "Hallo", "slug": "hallo"})

	_, html := c.do("GET", "/admin/c/post", nil)
	m := updatedRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatal("no update date in the post list")
	}
	shown, err := time.ParseInLocation("02.01.2006 15:04", m[1], loc)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(shown); d < -time.Minute || d > 2*time.Minute {
		t.Errorf("update date %q is %v off in %s", m[1], d.Round(time.Minute), zone)
	}
}
