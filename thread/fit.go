package thread

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf16"
)

const (
	maxText  = 4096
	maxSkips = maxText
	ellipsis = "…"
)

var tag = regexp.MustCompile(`<[^>]*>`)

func render(comment func(skip int) string) string {
	var prev string
	for skip := 0; skip <= maxSkips; skip++ {
		text := comment(skip)
		if textLen(text) <= maxText {
			return text
		}
		if skip > 0 && text == prev {
			break
		}
		prev = text
	}
	return fit(prev)
}

func fit(text string) string {
	if textLen(text) <= maxText {
		return text
	}
	budget := maxText - textLen(ellipsis)
	var b strings.Builder
	used := 0
	for _, r := range html.UnescapeString(tag.ReplaceAllString(text, "")) {
		escaped := html.EscapeString(string(r))
		n := textLen(escaped)
		if used+n > budget {
			break
		}
		b.WriteString(escaped)
		used += n
	}
	return b.String() + ellipsis
}

func textLen(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
