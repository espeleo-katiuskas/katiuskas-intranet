package publicsite

import (
	"regexp"
	"strings"
	"testing"
)

func TestRenderRichText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain text keeps its lines", "uno\ndos", `<p class="multiline">uno` + "\n" + `dos</p>`},
		{"a blank line ends a paragraph", "uno\n\ndos", `<p class="multiline">uno</p><p class="multiline">dos</p>`},
		{"headings are smaller than the page's own", "# A\n## B\n### C", "<h3>A</h3><h4>B</h4><h5>C</h5>"},
		{"a list", "- uno\n* dos", "<ul><li>uno</li><li>dos</li></ul>"},
		{"a rule", "---", "<hr>"},
		{"bold and italic", "**fuerte** y *suave*", `<p class="multiline"><strong>fuerte</strong> y <em>suave</em></p>`},
		{"arithmetic is not italics", "5 * 3 * 2", `<p class="multiline">5 * 3 * 2</p>`},
		{"a link", "[mapa](https://example.com/a?b=1&c=2)", `<p class="multiline"><a href="https://example.com/a?b=1&amp;c=2" target="_blank" rel="noopener noreferrer nofollow ugc">mapa</a></p>`},
		{"a bare address without the full stop that ends the sentence", "Mira https://example.com.", `<p class="multiline">Mira <a href="https://example.com" target="_blank" rel="noopener noreferrer nofollow ugc">https://example.com</a>.</p>`},
		{"mailto is allowed", "[escribir](mailto:a@b.es)", `<p class="multiline"><a href="mailto:a@b.es" target="_blank" rel="noopener noreferrer nofollow ugc">escribir</a></p>`},
	}
	for _, c := range cases {
		if got := string(renderRichText(c.in)); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// Nothing a person types can be a tag or an address that runs code: take away every tag that the
// formatter makes (an anchor only with its exact attributes) and no "<" is left, and every address of an
// anchor is http, https or mailto.
func TestRenderRichTextIsSafe(t *testing.T) {
	hostile := []string{
		"<script>alert(1)</script>",
		`<img src=x onerror=alert(1)>`,
		"[x](javascript:alert(1))",
		"[x](JaVaScRiPt:alert(1))",
		"[x](java\tscript:alert(1))",
		"[x](data:text/html;base64,PHNjcmlwdD4=)",
		"[x](//evil.example/x)",
		"[x](/relative/path)",
		"**<b onmouseover=alert(1)>**",
		"# <svg onload=alert(1)>",
		"- <iframe src=javascript:alert(1)>",
		"[<script>](https://example.com)",
		`[x](https://example.com" onclick="alert(1))`,
		"[a](https://example.com) [b](https://example.com/\u00a0x)",
		"*[<i>x</i>](https://example.com)*",
	}
	made := regexp.MustCompile(`</?(?:p|strong|em|ul|li|h[345])>|<p class="multiline">|<hr>|</a>|<a href="[^"<>]*" target="_blank" rel="noopener noreferrer nofollow ugc">`)
	hrefs := regexp.MustCompile(`<a href="([^"]*)"`)
	for _, in := range hostile {
		out := string(renderRichText(in))
		if rest := made.ReplaceAllString(out, ""); strings.Contains(rest, "<") {
			t.Errorf("%q gave %s: a tag that the formatter does not make", in, out)
		}
		for _, m := range hrefs.FindAllStringSubmatch(out, -1) {
			if h := m[1]; !strings.HasPrefix(h, "http://") && !strings.HasPrefix(h, "https://") && !strings.HasPrefix(h, "mailto:") {
				t.Errorf("%q made a link to %q", in, h)
			}
		}
	}
}

func TestPlacePathAndID(t *testing.T) {
	if got := placePath(12, "Sima de la Peña"); got != "/lugares/sima-de-la-pena-12" {
		t.Errorf("placePath: %q", got)
	}
	if got := placePath(3, "¿¿??"); got != "/lugares/lugar-3" {
		t.Errorf("a name with nothing to say: %q", got)
	}
	for slug, want := range map[string]int64{"sima-de-la-pena-12": 12, "cueva-2-30": 30, "12": 12, "lugar-3": 3} {
		if id, ok := placeIDOf(slug); !ok || id != want {
			t.Errorf("placeIDOf(%q) = %d %v, want %d", slug, id, ok, want)
		}
	}
	for _, slug := range []string{"", "sima", "sima-", "sima-0", "sima-012", "sima-1e3", "sima-99999999999999999999", "sima- 5"} {
		if id, ok := placeIDOf(slug); ok {
			t.Errorf("placeIDOf(%q) = %d, want nothing", slug, id)
		}
	}
}
