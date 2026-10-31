package tokens

import (
	"regexp"
	"strings"
)

// maxUserAgentLength is how much of a User-Agent header is stored: it comes from the client, so
// it is bounded (real ones are well under 300 characters).
const maxUserAgentLength = 512

// truncateUserAgent cuts a User-Agent to maxUserAgentLength characters.
func truncateUserAgent(ua string) string {
	if r := []rune(ua); len(r) > maxUserAgentLength {
		return string(r[:maxUserAgentLength])
	}
	return ua
}

// browserMarkers are looked for in this order, since most browsers claim to be several (Edge says
// it is Chrome, which says it is Safari): the first marker found names the browser, and the number
// after its slash is the version. A browser that has a marker of its own for each platform lists them
// all.
var browserMarkers = []struct {
	marker string
	name   string
}{
	{"Edg/", "Edge"}, {"EdgA/", "Edge"}, {"EdgiOS/", "Edge"},
	{"OPR/", "Opera"}, {"OPiOS/", "Opera"},
	{"SamsungBrowser/", "Samsung Internet"},
	{"Firefox/", "Firefox"}, {"FxiOS/", "Firefox"},
	{"Vivaldi/", "Vivaldi"},
	{"CriOS/", "Chrome"},
	{"Chromium/", "Chromium"},
	{"Chrome/", "Chrome"},
	{"curl/", "curl"},
}

var versionRe = regexp.MustCompile(`^(\d+)`)

// DetectClientFromUA works out the browser (its name and major version, "Firefox 120") and the
// operating system ("Windows", "Android", "iOS", "macOS", "Linux", "ChromeOS") from a User-Agent
// header; either is "" when it is not recognized. It is a best guess from a string the client
// chooses, good enough for an administrator to tell one session from another, never something to
// base a decision on. (The version of Windows and of macOS are not in the header any more —
// browsers freeze them — so only the family is given.)
func DetectClientFromUA(ua string) (browser, os string) {
	return detectBrowser(ua), detectOS(ua)
}

func detectBrowser(ua string) string {
	for _, m := range browserMarkers {
		if i := strings.Index(ua, m.marker); i >= 0 {
			if v := versionRe.FindString(ua[i+len(m.marker):]); v != "" {
				return m.name + " " + v
			}
			return m.name
		}
	}
	// Safari names its own version after "Version/", and has no marker of its own.
	if strings.Contains(ua, "Safari/") {
		if i := strings.Index(ua, "Version/"); i >= 0 {
			if v := versionRe.FindString(ua[i+len("Version/"):]); v != "" {
				return "Safari " + v
			}
		}
		return "Safari"
	}
	if i := strings.Index(ua, "MSIE "); i >= 0 {
		return "Internet Explorer " + versionRe.FindString(ua[i+len("MSIE "):])
	}
	if strings.Contains(ua, "Trident/") {
		return "Internet Explorer"
	}
	return ""
}

func detectOS(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		return "Windows"
	case strings.Contains(ua, "Android"): // before Linux: Android says it is Linux
		return "Android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"):
		return "iOS" // before macOS: they say "like Mac OS X"
	case strings.Contains(ua, "CrOS"):
		return "ChromeOS"
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		return "macOS"
	case strings.Contains(ua, "Linux"), strings.Contains(ua, "X11"):
		return "Linux"
	}
	return ""
}
