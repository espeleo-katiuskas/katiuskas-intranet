package publicsite

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// The free texts of a place (its description and its technical sheet) are written with the little
// formatting the intranet has (web/js/rich-text.js), and shown here with the same rules. It is not
// Markdown: only what is listed exists, and anything else stays as typed, with its line breaks.
//
//	# Title, ## Title, ### Title   headings
//	**bold**, *italic*             emphasis (it can contain links, but not other emphasis)
//	[text](https://…)              a link; a bare http(s) address is a link too
//	- item  or  * item             a bulleted list
//	---                            a horizontal rule
//
// Every span of literal text is escaped before any tag is written around it, and the only tags made are
// the fixed set below, so nothing that a person typed can inject HTML. A link is only made of an address
// that safeHref accepts, and it is marked as one that the site does not vouch for.

var (
	// In order: [text](address), a bare http(s) address, **bold**, *italic*. The emphasis may not start or end
	// with a space, so "5 * 3 * 2" is arithmetic and not italics.
	inlineRe  = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)|(https?://[^\s<>]+)|\*\*([^*\s](?:[^*\n]*[^*\s])?)\*\*|\*([^*\s](?:[^*\n]*[^*\s])?)\*`)
	headingRe = regexp.MustCompile(`^(#{1,3})\s+(.+)$`)
	itemRe    = regexp.MustCompile(`^[-*]\s+(.+)$`)
	ruleRe    = regexp.MustCompile(`^-{3,}$`)
	schemeRe  = regexp.MustCompile(`^(?i:([a-z][a-z0-9+.-]*):)`)
)

// safeHref is the address of a link as it can be put in an href, or "" if it must not be one: only http,
// https and mailto, and nothing with a space or a control character (browsers ignore tabs and line breaks
// inside an address, so "java<tab>script:" would be a javascript: one).
func safeHref(raw string) string {
	href := strings.TrimSpace(raw)
	if href == "" {
		return ""
	}
	for _, r := range href {
		if r <= 0x20 || r == 0x7f || r == 0xa0 || (r >= 0x2000 && r <= 0x200f) || r == 0x2028 || r == 0x2029 || r == 0x3000 {
			return ""
		}
	}
	m := schemeRe.FindStringSubmatch(href)
	if m == nil {
		return ""
	}
	switch strings.ToLower(m[1]) {
	case "http", "https", "mailto":
		return href
	}
	return ""
}

func anchor(href, labelHTML string) string {
	return `<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer nofollow ugc">` + labelHTML + `</a>`
}

// trimAddress drops the punctuation that ends a sentence, and a closing parenthesis that has no opening
// one in it, from the end of a bare address.
func trimAddress(address string) string {
	end := len(address)
	for end > 0 {
		last := address[end-1]
		opens := strings.Count(address[:end], "(")
		closes := strings.Count(address[:end], ")")
		if strings.ContainsRune(`.,;:!?'"`, rune(last)) || (last == ')' && closes > opens) {
			end--
			continue
		}
		break
	}
	return address[:end]
}

// renderInline is the text of a line as HTML; links says whether a link may be made here (not inside the
// text of another one).
func renderInline(text string, links bool) string {
	var out strings.Builder
	last := 0
	for _, m := range inlineRe.FindAllStringSubmatchIndex(text, -1) {
		out.WriteString(html.EscapeString(text[last:m[0]]))
		whole := text[m[0]:m[1]]
		group := func(n int) (string, bool) {
			if m[2*n] < 0 {
				return "", false
			}
			return text[m[2*n]:m[2*n+1]], true
		}
		switch {
		case m[2] >= 0: // [label](address)
			label, _ := group(1)
			address, _ := group(2)
			if href := safeHref(address); links && href != "" {
				out.WriteString(anchor(href, renderInline(label, false)))
			} else {
				out.WriteString(html.EscapeString(whole))
			}
		case m[6] >= 0: // a bare address
			bare, _ := group(3)
			address := trimAddress(bare)
			if href := safeHref(address); links && href != "" {
				out.WriteString(anchor(href, html.EscapeString(address)))
			} else {
				out.WriteString(html.EscapeString(address))
			}
			out.WriteString(html.EscapeString(bare[len(address):]))
		case m[8] >= 0: // **bold**
			bold, _ := group(4)
			out.WriteString("<strong>" + renderInline(bold, links) + "</strong>")
		default: // *italic*
			italic, _ := group(5)
			out.WriteString("<em>" + renderInline(italic, links) + "</em>")
		}
		last = m[1]
	}
	out.WriteString(html.EscapeString(text[last:]))
	return out.String()
}

// renderRichText is the text as HTML: the lines of a stretch of plain text stay as they are, and a blank
// line, a heading, a list or a rule ends the stretch. It is safe to put in a page: see the top of the file.
func renderRichText(source string) template.HTML {
	source = strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n")
	var parts []string
	var text []string
	var items []string
	flushText := func() {
		if len(text) > 0 {
			lines := make([]string, len(text))
			for i, l := range text {
				lines[i] = renderInline(l, true)
			}
			parts = append(parts, `<p class="multiline">`+strings.Join(lines, "\n")+`</p>`)
			text = nil
		}
	}
	flushList := func() {
		if items != nil {
			parts = append(parts, "<ul>"+strings.Join(items, "")+"</ul>")
			items = nil
		}
	}
	for _, raw := range strings.Split(source, "\n") {
		line := strings.TrimRight(raw, " \t")
		switch {
		case strings.TrimSpace(line) == "":
			flushText()
			flushList()
		case ruleRe.MatchString(strings.TrimSpace(line)):
			flushText()
			flushList()
			parts = append(parts, "<hr>")
		case headingRe.MatchString(line):
			m := headingRe.FindStringSubmatch(line)
			flushText()
			flushList()
			level := len(m[1]) + 2
			parts = append(parts, "<h"+string(rune('0'+level))+">"+renderInline(m[2], true)+"</h"+string(rune('0'+level))+">")
		case itemRe.MatchString(line):
			m := itemRe.FindStringSubmatch(line)
			flushText()
			if items == nil {
				items = []string{}
			}
			items = append(items, "<li>"+renderInline(m[1], true)+"</li>")
		default:
			flushList()
			text = append(text, line)
		}
	}
	flushText()
	flushList()
	return template.HTML(strings.Join(parts, ""))
}
