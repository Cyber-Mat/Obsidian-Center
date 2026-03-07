/**
 * CodeMirror 6 editor wrapper with Automerge CRDT integration.
 */

import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from "@codemirror/view";
import { EditorState, Transaction } from "@codemirror/state";
import { markdown, markdownLanguage } from "@codemirror/lang-markdown";
import { languages } from "@codemirror/language-data";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { syntaxHighlighting, defaultHighlightStyle, bracketMatching } from "@codemirror/language";
import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { oneDark } from "./theme";
import { CRDTManager } from "./sync/crdt";

export interface EditorCallbacks {
  onContentChange: (path: string, content: string) => void;
}

export class Editor {
  private view: EditorView | null = null;
  private container: HTMLElement;
  private emptyState: HTMLElement;
  private crdt: CRDTManager;
  private callbacks: EditorCallbacks;
  private currentPath: string | null = null;
  private suppressUpdates = false;

  constructor(
    container: HTMLElement,
    emptyState: HTMLElement,
    crdt: CRDTManager,
    callbacks: EditorCallbacks
  ) {
    this.container = container;
    this.emptyState = emptyState;
    this.crdt = crdt;
    this.callbacks = callbacks;
  }

  openFile(path: string): void {
    const content = this.crdt.getFileContent(path);
    if (content === null) return;

    this.currentPath = path;
    this.emptyState.hidden = true;

    if (this.view) {
      // Replace entire document content
      this.suppressUpdates = true;
      this.view.dispatch({
        changes: {
          from: 0,
          to: this.view.state.doc.length,
          insert: content,
        },
      });
      this.suppressUpdates = false;
    } else {
      this.createEditor(content);
    }
  }

  close(): void {
    this.currentPath = null;
    this.emptyState.hidden = false;
    if (this.view) {
      this.suppressUpdates = true;
      this.view.dispatch({
        changes: {
          from: 0,
          to: this.view.state.doc.length,
          insert: "",
        },
      });
      this.suppressUpdates = false;
    }
  }

  getCurrentPath(): string | null {
    return this.currentPath;
  }

  /**
   * Apply remote CRDT changes to the editor if the currently open file was affected.
   */
  applyRemoteChanges(affectedPaths: Set<string>): void {
    if (!this.currentPath || !affectedPaths.has(this.currentPath)) return;
    if (!this.view) return;

    const newContent = this.crdt.getFileContent(this.currentPath);
    if (newContent === null) {
      this.close();
      return;
    }

    const currentContent = this.view.state.doc.toString();
    if (currentContent === newContent) return;

    // Compute minimal diff to preserve cursor position and undo history
    const changes = computeMinimalChanges(currentContent, newContent);
    if (changes.length === 0) return;

    this.suppressUpdates = true;
    this.view.dispatch({
      changes,
      annotations: [Transaction.addToHistory.of(false)],
    });
    this.suppressUpdates = false;
  }

  private createEditor(content: string): void {
    const updateListener = EditorView.updateListener.of((update) => {
      if (this.suppressUpdates) return;
      if (!update.docChanged) return;
      if (!this.currentPath) return;

      const newContent = update.state.doc.toString();
      this.crdt.updateFileContent(this.currentPath, newContent);
      this.callbacks.onContentChange(this.currentPath, newContent);
    });

    const state = EditorState.create({
      doc: content,
      extensions: [
        lineNumbers(),
        highlightActiveLine(),
        highlightActiveLineGutter(),
        drawSelection(),
        bracketMatching(),
        closeBrackets(),
        history(),
        keymap.of([
          ...defaultKeymap,
          ...historyKeymap,
          ...closeBracketsKeymap,
        ]),
        markdown({ base: markdownLanguage, codeLanguages: languages }),
        syntaxHighlighting(defaultHighlightStyle),
        oneDark,
        updateListener,
        EditorView.lineWrapping,
      ],
    });

    this.view = new EditorView({
      state,
      parent: this.container,
    });
  }

  destroy(): void {
    if (this.view) {
      this.view.destroy();
      this.view = null;
    }
  }
}

/**
 * Compute a minimal set of changes between two strings by trimming common
 * prefix and suffix. This preserves cursor position and undo history
 * when applying remote CRDT changes.
 */
function computeMinimalChanges(
  oldText: string,
  newText: string
): { from: number; to: number; insert: string }[] {
  let prefixLen = 0;
  const minLen = Math.min(oldText.length, newText.length);
  while (prefixLen < minLen && oldText[prefixLen] === newText[prefixLen]) {
    prefixLen++;
  }

  let oldSuffix = oldText.length;
  let newSuffix = newText.length;
  while (
    oldSuffix > prefixLen &&
    newSuffix > prefixLen &&
    oldText[oldSuffix - 1] === newText[newSuffix - 1]
  ) {
    oldSuffix--;
    newSuffix--;
  }

  if (prefixLen === oldSuffix && prefixLen === newSuffix) return [];

  return [
    {
      from: prefixLen,
      to: oldSuffix,
      insert: newText.slice(prefixLen, newSuffix),
    },
  ];
}
