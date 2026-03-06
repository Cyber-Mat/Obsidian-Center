/**
 * Dark theme for CodeMirror 6, matching the Obsidian Center UI palette.
 */

import { EditorView } from "@codemirror/view";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags } from "@lezer/highlight";

const chalky = "#e5c07b";
const coral = "#e06c75";
const cyan = "#56b6c2";
const muted = "#6c7086";
const sage = "#98c379";
const violet = "#c678dd";
const blue = "#61afef";
const ivory = "#cdd6f4";
const stone = "#a6adc8";
const bg = "#1e1e2e";
const bgHighlight = "#313244";
const gutter = "#181825";
const selection = "rgba(137, 180, 250, 0.15)";

const themeBase = EditorView.theme(
  {
    "&": {
      color: ivory,
      backgroundColor: bg,
    },
    ".cm-content": {
      caretColor: "#89b4fa",
    },
    ".cm-cursor, .cm-dropCursor": {
      borderLeftColor: "#89b4fa",
    },
    "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection":
      {
        backgroundColor: selection,
      },
    ".cm-panels": {
      backgroundColor: gutter,
      color: ivory,
    },
    ".cm-searchMatch": {
      backgroundColor: "#45475a",
      outline: "1px solid #585b70",
    },
    ".cm-searchMatch.cm-searchMatch-selected": {
      backgroundColor: "#585b70",
    },
    ".cm-activeLine": {
      backgroundColor: "rgba(69, 71, 90, 0.3)",
    },
    ".cm-selectionMatch": {
      backgroundColor: "rgba(137, 180, 250, 0.1)",
    },
    "&.cm-focused .cm-matchingBracket, &.cm-focused .cm-nonmatchingBracket": {
      backgroundColor: "rgba(137, 180, 250, 0.2)",
    },
    ".cm-gutters": {
      backgroundColor: gutter,
      color: muted,
      border: "none",
    },
    ".cm-activeLineGutter": {
      backgroundColor: bgHighlight,
    },
    ".cm-foldPlaceholder": {
      backgroundColor: "transparent",
      border: "none",
      color: muted,
    },
    ".cm-tooltip": {
      border: "1px solid #45475a",
      backgroundColor: bgHighlight,
    },
    ".cm-tooltip .cm-tooltip-arrow:before": {
      borderTopColor: "transparent",
      borderBottomColor: "transparent",
    },
    ".cm-tooltip .cm-tooltip-arrow:after": {
      borderTopColor: bgHighlight,
      borderBottomColor: bgHighlight,
    },
    ".cm-tooltip-autocomplete": {
      "& > ul > li[aria-selected]": {
        backgroundColor: "rgba(137, 180, 250, 0.15)",
        color: ivory,
      },
    },
  },
  { dark: true }
);

const highlighting = HighlightStyle.define([
  { tag: tags.keyword, color: violet },
  { tag: [tags.name, tags.deleted, tags.character, tags.macroName], color: coral },
  { tag: [tags.function(tags.variableName), tags.labelName], color: blue },
  { tag: [tags.color, tags.constant(tags.name), tags.standard(tags.name)], color: chalky },
  { tag: [tags.definition(tags.name), tags.separator], color: ivory },
  {
    tag: [tags.typeName, tags.className, tags.number, tags.changed, tags.annotation, tags.modifier, tags.self, tags.namespace],
    color: chalky,
  },
  { tag: [tags.operator, tags.operatorKeyword, tags.url, tags.escape, tags.regexp, tags.link, tags.special(tags.string)], color: cyan },
  { tag: [tags.meta, tags.comment], color: muted },
  { tag: tags.strong, fontWeight: "bold" },
  { tag: tags.emphasis, fontStyle: "italic" },
  { tag: tags.strikethrough, textDecoration: "line-through" },
  { tag: tags.link, color: blue, textDecoration: "underline" },
  { tag: tags.heading, fontWeight: "bold", color: blue },
  { tag: [tags.atom, tags.bool, tags.special(tags.variableName)], color: chalky },
  { tag: [tags.processingInstruction, tags.string, tags.inserted], color: sage },
  { tag: tags.invalid, color: coral },
]);

export const oneDark = [themeBase, syntaxHighlighting(highlighting)];
