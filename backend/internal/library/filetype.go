package library

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// inlineTypes are the only types a browser is allowed to show inside the page; anything else is
// offered as a download, so a file can never run as a page of the intranet.
var inlineTypes = map[string]bool{
	"application/pdf": true, "image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

// Viewable says whether a stored content type is one the browser shows instead of downloading.
func Viewable(contentType string) bool { return inlineTypes[contentType] }

// isImage says whether a stored content type is one of the images a cover may be.
func isImage(contentType string) bool {
	return strings.HasPrefix(contentType, "image/") && inlineTypes[contentType]
}

// detectType decides a file's type from its bytes: a PDF or an image is recognised by content, so
// it can be shown; for anything else the name's extension gives a type that is only ever downloaded.
func detectType(name string, content []byte) string {
	sniffed, _, _ := strings.Cut(http.DetectContentType(content), ";")
	if inlineTypes[sniffed] {
		return sniffed
	}
	if byExt, _, err := mime.ParseMediaType(mime.TypeByExtension(filepath.Ext(name))); err == nil && !inlineTypes[byExt] {
		return byExt
	}
	return "application/octet-stream"
}
