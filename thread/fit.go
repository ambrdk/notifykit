package thread

import (
	"html"
	"regexp"
	"unicode/utf8"
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
		if utf8.RuneCountInString(text) <= maxText {
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
	if utf8.RuneCountInString(text) <= maxText {
		return text
	}
	plain := []rune(html.UnescapeString(tag.ReplaceAllString(text, "")))
	n := min(len(plain), maxText-1)
	for {
		out := html.EscapeString(string(plain[:n])) + ellipsis
		over := utf8.RuneCountInString(out) - maxText
		if over <= 0 {
			return out
		}
		n -= over
	}
}
