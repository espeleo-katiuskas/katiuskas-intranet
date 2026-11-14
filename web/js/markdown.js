import { escapeHtml } from './api.js';
import { isExternal, safeHref } from './rich-text.js';

// A small, dependency-free Markdown → HTML converter for the document
// archive's rendered documents (estatutos, actas...): headers,
// paragraphs, bold/italic, links, lists, and blockquotes — not a full
// CommonMark implementation, since club documents don't need tables or
// code fences. Every span of literal text is escaped *before* any
// markup is layered on top of it, so nothing in the source document can
// inject arbitrary HTML into the page — the only tags this ever
// produces are the fixed set written directly below.
//
// resolveLink(href) is called with each link's original target and
// must return the anchor's final href — the document archive page uses
// it to turn a document-relative link ("./Actas/", "../Estatutos.pdf")
// into an in-app navigation link instead of a raw filesystem path.
//
// A link only becomes an anchor if its final target passes safeHref
// (http, https, mailto, or a path): a "javascript:" or "data:" target is
// shown as the text it was written as, never as a link.

// renderInline works on text that is already escaped, and the target of
// a link has to be looked at and written from what the person typed.
function unescapeHtml(text) {
  return text
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&');
}

function renderInline(text, resolveLink) {
  let html = escapeHtml(text);
  html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  html = html.replace(/(?<!\*)\*([^*]+)\*(?!\*)/g, '<em>$1</em>');
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (match, label, escapedHref) => {
    const href = unescapeHtml(escapedHref);
    const finalHref = safeHref(resolveLink ? resolveLink(href) : href, { allowRelative: true });
    if (!finalHref) return match;
    const target = isExternal(finalHref) ? ' target="_blank" rel="noopener noreferrer"' : '';
    return `<a href="${escapeHtml(finalHref)}"${target}>${label}</a>`;
  });
  return html;
}

function renderMarkdown(source, resolveLink) {
  const lines = source.replace(/\r\n/g, '\n').split('\n');
  const htmlParts = [];
  let paraLines = [];
  let listItems = null;
  let listTag = null;

  function flushParagraph() {
    if (paraLines.length) {
      htmlParts.push(`<p>${renderInline(paraLines.join(' '), resolveLink)}</p>`);
      paraLines = [];
    }
  }
  function flushList() {
    if (listItems) {
      htmlParts.push(`<${listTag}>${listItems.join('')}</${listTag}>`);
      listItems = null;
      listTag = null;
    }
  }
  function flushAll() {
    flushParagraph();
    flushList();
  }

  for (const rawLine of lines) {
    const line = rawLine.trimEnd();
    if (line.trim() === '') {
      flushAll();
      continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      flushAll();
      const level = heading[1].length;
      htmlParts.push(`<h${level}>${renderInline(heading[2], resolveLink)}</h${level}>`);
      continue;
    }

    const unordered = line.match(/^[-*]\s+(.*)$/);
    if (unordered) {
      flushParagraph();
      if (listTag !== 'ul') {
        flushList();
        listTag = 'ul';
        listItems = [];
      }
      listItems.push(`<li>${renderInline(unordered[1], resolveLink)}</li>`);
      continue;
    }

    const ordered = line.match(/^\d+\.\s+(.*)$/);
    if (ordered) {
      flushParagraph();
      if (listTag !== 'ol') {
        flushList();
        listTag = 'ol';
        listItems = [];
      }
      listItems.push(`<li>${renderInline(ordered[1], resolveLink)}</li>`);
      continue;
    }

    const quote = line.match(/^>\s?(.*)$/);
    if (quote) {
      flushAll();
      htmlParts.push(`<blockquote>${renderInline(quote[1], resolveLink)}</blockquote>`);
      continue;
    }

    flushList();
    paraLines.push(line);
  }
  flushAll();

  return htmlParts.join('\n');
}

export { renderMarkdown };
