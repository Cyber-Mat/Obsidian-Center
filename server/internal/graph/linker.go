package graph

import (
	"regexp"
	"strings"
)

// Link represents a parsed wiki-link or embed from a markdown file.
type Link struct {
	TargetPath string // Resolved file path (e.g., "folder/note.md")
	LinkText   string // Display text from [[target|display]]
	Anchor     string // Heading anchor from [[note#heading]]
	IsEmbed    bool   // true for ![[embeds]]
	Position   int    // Byte offset in source file
}

// wikiLinkRe matches both regular wiki-links and embeds:
//
//	[[target]]
//	[[target|display text]]
//	[[target#heading]]
//	[[target#heading|display text]]
//	![[target]]
//	![[target|display text]]
var wikiLinkRe = regexp.MustCompile(`(!?)\[\[([^\[\]]+?)\]\]`)

// codeBlockRe matches fenced code blocks (``` or ~~~).
var codeBlockRe = regexp.MustCompile("(?m)^(```|~~~).*$")

// inlineCodeRe matches inline code spans.
var inlineCodeRe = regexp.MustCompile("`[^`]+`")

// ParseLinks extracts all wiki-links from markdown content.
// It returns the links found with their resolved target paths.
// existingPaths is used to resolve ambiguous links (basename matching).
// Links inside code blocks and inline code are skipped.
func ParseLinks(content string, existingPaths []string) []Link {
	excluded := buildExcludedRanges(content)
	matches := wikiLinkRe.FindAllStringSubmatchIndex(content, -1)
	links := make([]Link, 0, len(matches))

	for _, match := range matches {
		position := match[0]
		if isInRange(position, excluded) {
			continue
		}

		isEmbed := content[match[2]:match[3]] == "!"
		inner := content[match[4]:match[5]]

		link := parseInner(inner, isEmbed, position, existingPaths)
		links = append(links, link)
	}

	return links
}

// buildExcludedRanges returns byte ranges that should be excluded from
// link parsing (fenced code blocks and inline code spans).
func buildExcludedRanges(content string) [][2]int {
	var ranges [][2]int

	// Find fenced code blocks
	fenceMatches := codeBlockRe.FindAllStringIndex(content, -1)
	for i := 0; i+1 < len(fenceMatches); i += 2 {
		ranges = append(ranges, [2]int{fenceMatches[i][0], fenceMatches[i+1][1]})
	}

	// Find inline code
	for _, m := range inlineCodeRe.FindAllStringIndex(content, -1) {
		ranges = append(ranges, [2]int{m[0], m[1]})
	}

	return ranges
}

func isInRange(pos int, ranges [][2]int) bool {
	for _, r := range ranges {
		if pos >= r[0] && pos < r[1] {
			return true
		}
	}
	return false
}

// parseInner parses the inner content of a wiki-link (between [[ and ]]).
func parseInner(inner string, isEmbed bool, position int, existingPaths []string) Link {
	link := Link{
		IsEmbed:  isEmbed,
		Position: position,
	}

	// Split on | for display text
	if idx := strings.Index(inner, "|"); idx >= 0 {
		link.LinkText = inner[idx+1:]
		inner = inner[:idx]
	}

	// Split on # for anchor
	if idx := strings.Index(inner, "#"); idx >= 0 {
		link.Anchor = inner[idx+1:]
		inner = inner[:idx]
	}

	// Resolve the target path
	link.TargetPath = resolveTarget(strings.TrimSpace(inner), existingPaths)

	return link
}

// resolveTarget resolves a wiki-link target to a file path.
// Obsidian uses shortest-unique-path matching:
//   - "note" matches "note.md" or "folder/note.md"
//   - "folder/note" matches "folder/note.md"
//   - If the target already has an extension, use it as-is
func resolveTarget(target string, existingPaths []string) string {
	if target == "" {
		return ""
	}

	// If the target already has a known extension, use as-is
	if hasKnownExtension(target) {
		return target
	}

	// Try adding .md
	withMD := target + ".md"

	// First: exact match
	for _, p := range existingPaths {
		if p == withMD || p == target {
			return p
		}
	}

	// Second: basename match (Obsidian's shortest-path resolution)
	targetBase := baseName(withMD)
	for _, p := range existingPaths {
		if baseName(p) == targetBase {
			return p
		}
	}

	// Not found — return with .md appended (best guess)
	return withMD
}

func hasKnownExtension(path string) bool {
	exts := []string{
		".md", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg",
		".pdf", ".mp3", ".mp4", ".wav", ".ogg", ".zip", ".tar", ".gz",
		".css", ".js", ".html", ".canvas",
	}
	lower := strings.ToLower(path)
	for _, ext := range exts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func baseName(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
