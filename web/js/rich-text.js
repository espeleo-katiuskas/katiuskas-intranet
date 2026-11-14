import { escapeHtml } from './api.js';

// A small, dependency-free formatter for the free texts people write (the description of an
// activity, of a distribution or of a library item, and the comments of the library). It is not
// Markdown: only what is listed here exists, and anything else stays as typed, with its line breaks.
//
//   # Title, ## Title, ### Title   headings (shown smaller than the page's own title)
//   **bold**, *italic*              emphasis (it can contain links, but not other emphasis)
//   [text](https://…)               a link; a bare http(s) address is a link too
//   - item  or  * item              a bulleted list
//   ---                             a horizontal rule
//
// Every span of literal text is escaped *before* any tag is written around it, and the only tags
// this produces are the fixed set below, so nothing a person types can inject HTML. A link is only
// made of an address that safeHref accepts.

const ALLOWED_SCHEMES = ['http', 'https', 'mailto'];

function hasSpaceOrControl(text) {
  for (const char of text) {
    const code = char.codePointAt(0);
    if (code <= 0x20 || code === 0x7f || /\s/.test(char)) return true;
  }
  return false;
}

// The address of a link as it can be put in an href, or null if it must not be one: only http,
// https and mailto links, and addresses of the intranet itself ("/actividad?id=3"). With
// allowRelative a path that is relative to the page ("./Actas/") is accepted too, for the texts
// whose own code resolves them. Anything with a space or a control character is refused: browsers
// ignore tabs and line breaks inside an address, so "java<tab>script:" would be a javascript: one.
function safeHref(raw, { allowRelative = false } = {}) {
  const href = String(raw ?? '').trim();
  if (!href || hasSpaceOrControl(href)) return null;
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(href);
  if (scheme) return ALLOWED_SCHEMES.includes(scheme[1].toLowerCase()) ? href : null;
  if (allowRelative) return href;
  return href.startsWith('/') && !href.startsWith('//') ? href : null;
}

// Whether a link leaves the intranet (and so opens in a new tab).
function isExternal(href) {
  return /^(https?:)?\/\//i.test(href);
}

function anchor(href, labelHtml) {
  const target = isExternal(href) ? ' target="_blank" rel="noopener noreferrer"' : '';
  return `<a href="${escapeHtml(href)}"${target}>${labelHtml}</a>`;
}

// In order: [text](address), a bare http(s) address, **bold**, *italic*. The emphasis may not start
// or end with a space, so "5 * 3 * 2" is arithmetic and not italics.
const INLINE =
  /\[([^\]\n]+)\]\(([^)\s]+)\)|(https?:\/\/[^\s<>]+)|\*\*(?!\s)([^*\n]*[^*\s])\*\*|\*(?!\s)([^*\n]*[^*\s])\*/g;

// The punctuation that ends a sentence is not part of a bare address, nor is a closing parenthesis
// that has no opening one in it.
function trimAddress(address) {
  let end = address.length;
  while (end > 0) {
    const last = address[end - 1];
    const opens = address.slice(0, end).split('(').length - 1;
    const closes = address.slice(0, end).split(')').length - 1;
    if (/[.,;:!?'"]/.test(last) || (last === ')' && closes > opens)) end -= 1;
    else break;
  }
  return address.slice(0, end);
}

// links: whether a link may be made here (not inside the text of another one).
function renderInline(text, links = true) {
  const re = new RegExp(INLINE.source, 'g');
  let html = '';
  let last = 0;
  let match = re.exec(text);
  while (match) {
    html += escapeHtml(text.slice(last, match.index));
    const [whole, label, address, bare, bold, italic] = match;
    if (label !== undefined) {
      const href = links ? safeHref(address) : null;
      html += href ? anchor(href, renderInline(label, false)) : escapeHtml(whole);
    } else if (bare !== undefined) {
      const address2 = trimAddress(bare);
      const href = links ? safeHref(address2) : null;
      html += href ? anchor(href, escapeHtml(address2)) : escapeHtml(address2);
      html += escapeHtml(bare.slice(address2.length));
    } else if (bold !== undefined) {
      html += `<strong>${renderInline(bold, links)}</strong>`;
    } else {
      html += `<em>${renderInline(italic, links)}</em>`;
    }
    last = re.lastIndex;
    match = re.exec(text);
  }
  return html + escapeHtml(text.slice(last));
}

// The text as HTML: the lines of a stretch of plain text stay as they are (the .multiline class
// keeps them), and a blank line, a heading, a list or a rule ends the stretch. Put it inside an
// element with the .rich-text class.
function renderRichText(source) {
  const lines = String(source ?? '')
    .replace(/\r\n?/g, '\n')
    .split('\n');
  const parts = [];
  let text = [];
  let items = null;

  function flushText() {
    if (text.length) {
      parts.push(`<p class="multiline">${text.map((line) => renderInline(line)).join('\n')}</p>`);
      text = [];
    }
  }
  function flushList() {
    if (items) {
      parts.push(`<ul>${items.join('')}</ul>`);
      items = null;
    }
  }

  for (const rawLine of lines) {
    const line = rawLine.trimEnd();
    if (line.trim() === '') {
      flushText();
      flushList();
      continue;
    }
    if (/^-{3,}$/.test(line.trim())) {
      flushText();
      flushList();
      parts.push('<hr>');
      continue;
    }
    const heading = /^(#{1,3})\s+(.+)$/.exec(line);
    if (heading) {
      flushText();
      flushList();
      const level = heading[1].length + 2;
      parts.push(`<h${level}>${renderInline(heading[2])}</h${level}>`);
      continue;
    }
    const item = /^[-*]\s+(.+)$/.exec(line);
    if (item) {
      flushText();
      items ??= [];
      items.push(`<li>${renderInline(item[1])}</li>`);
      continue;
    }
    flushList();
    text.push(line);
  }
  flushText();
  flushList();
  return parts.join('');
}

export { isExternal, renderRichText, safeHref };
