package graph

import (
	"testing"
)

func TestParseLinks(t *testing.T) {
	existingPaths := []string{
		"note.md",
		"folder/note.md",
		"another.md",
		"image.png",
	}

	tests := []struct {
		name     string
		content  string
		expected []Link
	}{
		{
			name:    "simple wiki link",
			content: "See [[note]] for details",
			expected: []Link{
				{TargetPath: "note.md", Position: 4},
			},
		},
		{
			name:    "wiki link with display text",
			content: "See [[note|my note]] for details",
			expected: []Link{
				{TargetPath: "note.md", LinkText: "my note", Position: 4},
			},
		},
		{
			name:    "wiki link with anchor",
			content: "See [[note#heading]] for details",
			expected: []Link{
				{TargetPath: "note.md", Anchor: "heading", Position: 4},
			},
		},
		{
			name:    "wiki link with anchor and display text",
			content: "See [[note#heading|display]] for details",
			expected: []Link{
				{TargetPath: "note.md", Anchor: "heading", LinkText: "display", Position: 4},
			},
		},
		{
			name:    "embed",
			content: "![[image.png]]",
			expected: []Link{
				{TargetPath: "image.png", IsEmbed: true, Position: 0},
			},
		},
		{
			name:    "embed markdown",
			content: "![[note]]",
			expected: []Link{
				{TargetPath: "note.md", IsEmbed: true, Position: 0},
			},
		},
		{
			name:    "folder path",
			content: "[[folder/note]]",
			expected: []Link{
				{TargetPath: "folder/note.md", Position: 0},
			},
		},
		{
			name:    "multiple links",
			content: "See [[note]] and [[another]]",
			expected: []Link{
				{TargetPath: "note.md", Position: 4},
				{TargetPath: "another.md", Position: 17},
			},
		},
		{
			name:     "no links",
			content:  "Just plain text with no links",
			expected: []Link{},
		},
		{
			name:    "unresolved link gets .md",
			content: "[[nonexistent]]",
			expected: []Link{
				{TargetPath: "nonexistent.md", Position: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			links := ParseLinks(tt.content, existingPaths)

			if len(links) != len(tt.expected) {
				t.Fatalf("expected %d links, got %d: %+v", len(tt.expected), len(links), links)
			}

			for i, expected := range tt.expected {
				got := links[i]
				if got.TargetPath != expected.TargetPath {
					t.Errorf("link %d: target = %q, want %q", i, got.TargetPath, expected.TargetPath)
				}
				if got.LinkText != expected.LinkText {
					t.Errorf("link %d: link_text = %q, want %q", i, got.LinkText, expected.LinkText)
				}
				if got.Anchor != expected.Anchor {
					t.Errorf("link %d: anchor = %q, want %q", i, got.Anchor, expected.Anchor)
				}
				if got.IsEmbed != expected.IsEmbed {
					t.Errorf("link %d: is_embed = %v, want %v", i, got.IsEmbed, expected.IsEmbed)
				}
				if got.Position != expected.Position {
					t.Errorf("link %d: position = %d, want %d", i, got.Position, expected.Position)
				}
			}
		})
	}
}

func TestResolveTarget(t *testing.T) {
	paths := []string{"notes/daily/2024-01-01.md", "projects/readme.md", "image.png"}

	tests := []struct {
		target   string
		expected string
	}{
		{"daily/2024-01-01", "notes/daily/2024-01-01.md"},    // basename match
		{"readme", "projects/readme.md"},                      // basename match
		{"image.png", "image.png"},                             // has extension
		{"unknown", "unknown.md"},                              // not found → add .md
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got := resolveTarget(tt.target, paths)
			if got != tt.expected {
				t.Errorf("resolveTarget(%q) = %q, want %q", tt.target, got, tt.expected)
			}
		})
	}
}
