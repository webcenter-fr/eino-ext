package websearch

import (
	"fmt"
	"io"
	"strings"

	"emperror.dev/errors"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"golang.org/x/net/html"
)

// stripTagsToText converts HTML to plain text by stripping tags and removing
// script, style, noscript, and iframe content entirely.
func stripTagsToText(body string) (string, error) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return "", errors.Wrap(err, "failed to parse HTML for text extraction")
	}

	var sb strings.Builder
	extractText(doc, &sb)
	return strings.TrimSpace(sb.String()), nil
}

// extractText walks the HTML tree and extracts text content, skipping
// non-content elements (script, style, noscript, iframe).
func extractText(n *html.Node, sb *strings.Builder) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "noscript", "iframe", "head", "svg":
			return
		}
	}
	if n.Type == html.TextNode {
		text := strings.TrimSpace(n.Data)
		if text != "" {
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(text)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractText(c, sb)
	}
	// Add newlines after block-level elements for readability.
	if n.Type == html.ElementNode {
		switch n.Data {
		case "p", "div", "li", "h1", "h2", "h3", "h4", "h5", "h6", "br", "tr", "hr":
			sb.WriteByte('\n')
		}
	}
}

// htmlToMarkdown converts HTML content to Markdown using the html-to-markdown library.
func htmlToMarkdown(htmlContent string) (string, error) {
	result, err := htmltomarkdown.ConvertString(htmlContent)
	if err != nil {
		return "", errors.Wrap(err, "failed to convert HTML to Markdown")
	}
	return result, nil
}

// readLimitedBody reads from an io.Reader up to maxSize bytes.
// It returns an error if the body exceeds maxSize.
func readLimitedBody(r io.Reader, maxSize int64) ([]byte, error) {
	// Use LimitReader plus a small probe to detect overrun.
	lr := io.LimitReader(r, maxSize+1)
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}
	if int64(len(body)) > maxSize {
		return nil, fmt.Errorf("response body exceeds maximum allowed size of %d bytes", maxSize)
	}
	return body, nil
}
