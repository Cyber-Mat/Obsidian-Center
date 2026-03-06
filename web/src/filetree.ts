/**
 * File tree UI component for the sidebar.
 * Renders a hierarchical folder/file structure from a flat list of paths.
 */

export interface FileTreeCallbacks {
  onFileSelect: (path: string) => void;
}

interface TreeNode {
  name: string;
  path: string;
  isFolder: boolean;
  children: TreeNode[];
}

export class FileTree {
  private container: HTMLElement;
  private callbacks: FileTreeCallbacks;
  private activePath: string | null = null;

  constructor(container: HTMLElement, callbacks: FileTreeCallbacks) {
    this.container = container;
    this.callbacks = callbacks;
  }

  render(paths: string[]): void {
    const root = buildTree(paths);
    this.container.innerHTML = "";
    this.renderNodes(root.children, this.container);
  }

  setActive(path: string | null): void {
    this.activePath = path;

    // Update active styling
    const items = this.container.querySelectorAll(".file-item");
    items.forEach((el) => {
      const itemPath = (el as HTMLElement).dataset.path;
      el.classList.toggle("active", itemPath === path);
    });
  }

  private renderNodes(nodes: TreeNode[], parent: HTMLElement): void {
    // Sort: folders first, then alphabetical
    const sorted = [...nodes].sort((a, b) => {
      if (a.isFolder !== b.isFolder) return a.isFolder ? -1 : 1;
      return a.name.localeCompare(b.name);
    });

    for (const node of sorted) {
      if (node.isFolder) {
        this.renderFolder(node, parent);
      } else {
        this.renderFile(node, parent);
      }
    }
  }

  private renderFolder(node: TreeNode, parent: HTMLElement): void {
    const folderEl = document.createElement("div");
    folderEl.className = "folder-item";
    folderEl.textContent = node.name;

    const childrenEl = document.createElement("div");
    childrenEl.className = "folder-children";

    folderEl.addEventListener("click", () => {
      childrenEl.classList.toggle("collapsed");
    });

    parent.appendChild(folderEl);
    parent.appendChild(childrenEl);
    this.renderNodes(node.children, childrenEl);
  }

  private renderFile(node: TreeNode, parent: HTMLElement): void {
    const fileEl = document.createElement("div");
    fileEl.className = "file-item";
    if (node.path === this.activePath) {
      fileEl.classList.add("active");
    }
    fileEl.dataset.path = node.path;
    fileEl.textContent = node.name;

    fileEl.addEventListener("click", () => {
      this.setActive(node.path);
      this.callbacks.onFileSelect(node.path);
    });

    parent.appendChild(fileEl);
  }
}

function buildTree(paths: string[]): TreeNode {
  const root: TreeNode = { name: "", path: "", isFolder: true, children: [] };

  for (const filePath of paths) {
    const parts = filePath.split("/");
    let current = root;

    for (let i = 0; i < parts.length; i++) {
      const part = parts[i];
      const isLast = i === parts.length - 1;
      const existingChild = current.children.find((c) => c.name === part);

      if (existingChild) {
        current = existingChild;
      } else {
        const newNode: TreeNode = {
          name: part,
          path: isLast ? filePath : parts.slice(0, i + 1).join("/"),
          isFolder: !isLast,
          children: [],
        };
        current.children.push(newNode);
        current = newNode;
      }
    }
  }

  return root;
}
