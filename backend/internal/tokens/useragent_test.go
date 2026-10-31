package tokens_test

import (
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
)

func TestDetectClientFromUA(t *testing.T) {
	cases := []struct {
		name, ua, browser, os string
	}{
		{"Chrome on Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36", "Chrome 118", "Windows"},
		{"Edge on Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36 Edg/118.0.2088.46", "Edge 118", "Windows"},
		{"Firefox on Linux", "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0", "Firefox 120", "Linux"},
		{"Firefox on Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/115.0", "Firefox 115", "Windows"},
		{"Safari on macOS", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15", "Safari 17", "macOS"},
		{"Chrome on macOS", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36", "Chrome 119", "macOS"},
		{"Safari on iPhone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", "Safari 17", "iOS"},
		{"Chrome on iPhone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/118.0.5993.69 Mobile/15E148 Safari/604.1", "Chrome 118", "iOS"},
		{"Firefox on iPad", "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/120.0 Mobile/15E148 Safari/605.1.15", "Firefox 120", "iOS"},
		{"Chrome on Android", "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Mobile Safari/537.36", "Chrome 118", "Android"},
		{"Samsung Internet", "Mozilla/5.0 (Linux; Android 13; SAMSUNG SM-S911B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/23.0 Chrome/115.0.0.0 Mobile Safari/537.36", "Samsung Internet 23", "Android"},
		{"Firefox on Android", "Mozilla/5.0 (Android 13; Mobile; rv:120.0) Gecko/120.0 Firefox/120.0", "Firefox 120", "Android"},
		{"Opera", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36 OPR/104.0.0.0", "Opera 104", "Windows"},
		{"Chrome on ChromeOS", "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36", "Chrome 118", "ChromeOS"},
		{"Internet Explorer 11", "Mozilla/5.0 (Windows NT 6.1; WOW64; Trident/7.0; rv:11.0) like Gecko", "Internet Explorer", "Windows"},
		{"curl", "curl/8.4.0", "curl 8", ""},
		{"nothing recognizable", "SomeBot", "", ""},
		{"empty", "", "", ""},
	}
	for _, c := range cases {
		browser, os := tokens.DetectClientFromUA(c.ua)
		if browser != c.browser || os != c.os {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, browser, os, c.browser, c.os)
		}
	}
}

func TestDetectClientFromUAIsHarmlessOnHostileInput(t *testing.T) {
	for _, ua := range []string{
		strings.Repeat("Chrome/", 5000), "Chrome/", "Firefox/x", "Version/ Safari/", "MSIE ", "\x00\xff\xfe", strings.Repeat("(", 100000),
	} {
		tokens.DetectClientFromUA(ua) // must not panic
	}
}
