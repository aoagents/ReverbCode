import React from "react";
import { Link, useLocation } from "react-router-dom";
import { ArrowLeft, BookOpen, ChevronRight, ExternalLink, Search } from "lucide-react";

const DOC_SECTIONS = [
  {
    title: "Getting Started",
    pages: [
      ["index", "Introduction"],
      ["installation", "Installation"],
      ["quickstart", "Quickstart"],
      ["platforms", "Platforms"],
    ],
  },
  {
    title: "Learn",
    pages: [
      ["guides", "Guides"],
      ["guides/parallel-issues", "Parallel issues"],
      ["guides/review-loop", "Review loop"],
      ["plugins", "Plugins"],
      ["plugins/agents", "Agents"],
      ["plugins/notifiers", "Notifiers"],
    ],
  },
  {
    title: "Reference",
    pages: [
      ["cli", "CLI"],
      ["configuration", "Configuration"],
      ["architecture", "Architecture"],
      ["examples", "Examples"],
      ["dashboard", "Dashboard"],
    ],
  },
  {
    title: "Help",
    pages: [
      ["troubleshooting", "Troubleshooting"],
      ["faq", "FAQ"],
      ["migration", "Migration"],
      ["changelog", "Changelog"],
    ],
  },
];

const FLAT_DOCS = DOC_SECTIONS.flatMap((section) => section.pages);

function normalizeSlug(pathname) {
  const slug = pathname.replace(/^\/docs\/?/, "").replace(/\/$/, "");
  return slug || "index";
}

function getDocPath(slug) {
  if (slug === "index") return "/docs/index.mdx";
  return `/docs/${slug}.mdx`;
}

function getIndexDocPath(slug) {
  return `/docs/${slug}/index.mdx`;
}

function parseFrontmatter(raw) {
  if (!raw.startsWith("---")) return { meta: {}, body: raw };
  const end = raw.indexOf("\n---", 3);
  if (end === -1) return { meta: {}, body: raw };
  const frontmatter = raw.slice(3, end).trim();
  const body = raw.slice(end + 4).trimStart();
  const meta = {};
  frontmatter.split("\n").forEach((line) => {
    const [key, ...value] = line.split(":");
    if (key && value.length) meta[key.trim()] = value.join(":").trim();
  });
  return { meta, body };
}

function cleanMdx(body) {
  return body
    .replace(/^import .+;?\n/gm, "")
    .replace(/<Callout[\s\S]*?<\/Callout>/g, "")
    .replace(/<Cards>[\s\S]*?<\/Cards>/g, "")
    .replace(/<PlatformSupport[\s\S]*?\/>/g, "")
    .replace(/<[^>]+>/g, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

function inlineParts(text) {
  const parts = [];
  const regex = /(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\([^)]+\))/g;
  let lastIndex = 0;
  let match;

  while ((match = regex.exec(text)) !== null) {
    if (match.index > lastIndex) parts.push(text.slice(lastIndex, match.index));
    const token = match[0];
    if (token.startsWith("`")) {
      parts.push(
        <code key={`${token}-${match.index}`} className="docs-inline-code">
          {token.slice(1, -1)}
        </code>
      );
    } else if (token.startsWith("**")) {
      parts.push(<strong key={`${token}-${match.index}`}>{token.slice(2, -2)}</strong>);
    } else {
      const link = token.match(/\[([^\]]+)\]\(([^)]+)\)/);
      if (link) {
        const href = link[2].replace(/^\/docs$/, "/docs").replace(/^\/docs\//, "/docs/");
        const external = href.startsWith("http");
        parts.push(
          <a
            key={`${token}-${match.index}`}
            href={href}
            target={external ? "_blank" : undefined}
            rel={external ? "noreferrer" : undefined}
          >
            {link[1]}
            {external && <ExternalLink className="inline-block h-3 w-3 ml-1" />}
          </a>
        );
      }
    }
    lastIndex = regex.lastIndex;
  }

  if (lastIndex < text.length) parts.push(text.slice(lastIndex));
  return parts;
}

function renderMarkdown(body) {
  const lines = cleanMdx(body).split("\n");
  const elements = [];
  let listItems = [];
  let tableRows = [];
  let codeLines = [];
  let inCode = false;
  let codeLanguage = "";

  const flushList = () => {
    if (!listItems.length) return;
    elements.push(
      <ul key={`list-${elements.length}`} className="docs-list">
        {listItems.map((item, index) => (
          <li key={`${item}-${index}`}>{inlineParts(item)}</li>
        ))}
      </ul>
    );
    listItems = [];
  };

  const flushTable = () => {
    if (tableRows.length < 2) {
      tableRows = [];
      return;
    }
    const [head, separator, ...rows] = tableRows;
    if (!separator.includes("---")) {
      tableRows = [];
      return;
    }
    elements.push(
      <div key={`table-${elements.length}`} className="docs-table-wrap">
        <table>
          <thead>
            <tr>
              {head.map((cell) => (
                <th key={cell}>{inlineParts(cell)}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, rowIndex) => (
              <tr key={rowIndex}>
                {row.map((cell, cellIndex) => (
                  <td key={`${rowIndex}-${cellIndex}`}>{inlineParts(cell)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
    tableRows = [];
  };

  lines.forEach((line, index) => {
    if (line.startsWith("```")) {
      if (inCode) {
        elements.push(
          <pre key={`code-${elements.length}`} className="docs-code">
            {codeLanguage && <span className="docs-code-lang">{codeLanguage}</span>}
            <code>{codeLines.join("\n")}</code>
          </pre>
        );
        codeLines = [];
        codeLanguage = "";
        inCode = false;
      } else {
        flushList();
        flushTable();
        inCode = true;
        codeLanguage = line.replace("```", "").trim();
      }
      return;
    }

    if (inCode) {
      codeLines.push(line);
      return;
    }

    if (/^\|.+\|$/.test(line.trim())) {
      flushList();
      tableRows.push(
        line
          .trim()
          .slice(1, -1)
          .split("|")
          .map((cell) => cell.trim())
      );
      return;
    }

    flushTable();

    if (!line.trim()) {
      flushList();
      return;
    }

    if (line.startsWith("# ")) {
      flushList();
      elements.push(<h1 key={index}>{inlineParts(line.slice(2))}</h1>);
    } else if (line.startsWith("## ")) {
      flushList();
      elements.push(<h2 key={index}>{inlineParts(line.slice(3))}</h2>);
    } else if (line.startsWith("### ")) {
      flushList();
      elements.push(<h3 key={index}>{inlineParts(line.slice(4))}</h3>);
    } else if (line.startsWith("- ")) {
      listItems.push(line.slice(2));
    } else if (/^\d+\.\s/.test(line)) {
      listItems.push(line.replace(/^\d+\.\s/, ""));
    } else {
      flushList();
      elements.push(<p key={index}>{inlineParts(line)}</p>);
    }
  });

  flushList();
  flushTable();

  return elements;
}

export default function DocsPage() {
  const location = useLocation();
  const slug = normalizeSlug(location.pathname);
  const [doc, setDoc] = React.useState({ status: "loading", meta: {}, body: "" });

  React.useEffect(() => {
    document.title = "Docs - Agent Orchestrator";
    const savedTheme = window.localStorage.getItem("ao-theme") || "dark";
    document.documentElement.dataset.theme = savedTheme;
  }, []);

  React.useEffect(() => {
    let cancelled = false;
    setDoc({ status: "loading", meta: {}, body: "" });

    async function loadDoc() {
      const primary = await fetch(getDocPath(slug));
      const response = primary.ok ? primary : await fetch(getIndexDocPath(slug));

      if (!response.ok) {
        if (!cancelled) setDoc({ status: "missing", meta: { title: "Not found" }, body: "" });
        return;
      }

      const raw = await response.text();
      const parsed = parseFrontmatter(raw);
      if (!cancelled) setDoc({ status: "ready", ...parsed });
    }

    loadDoc();
    return () => {
      cancelled = true;
    };
  }, [slug]);

  const title = doc.meta.title || FLAT_DOCS.find(([page]) => page === slug)?.[1] || "Docs";
  const activeIndex = FLAT_DOCS.findIndex(([page]) => page === slug);
  const nextDoc = activeIndex >= 0 ? FLAT_DOCS[activeIndex + 1] : null;

  return (
    <div className="docs-shell">
      <header className="docs-topbar">
        <div className="container-page flex h-16 items-center justify-between gap-4">
          <Link to="/" className="docs-back">
            <ArrowLeft className="h-4 w-4" />
            Agent Orchestrator
          </Link>
          <a
            href="https://github.com/aoagents/ReverbCode"
            target="_blank"
            rel="noreferrer"
            className="docs-repo-link"
          >
            GitHub
            <ExternalLink className="h-3.5 w-3.5" />
          </a>
        </div>
      </header>

      <div className="container-page docs-layout">
        <aside className="docs-sidebar">
          <div className="docs-search">
            <Search className="h-4 w-4" />
            <span>Documentation</span>
          </div>
          {DOC_SECTIONS.map((section) => (
            <div key={section.title} className="docs-nav-section">
              <h2>{section.title}</h2>
              {section.pages.map(([page, label]) => (
                <Link
                  key={page}
                  to={page === "index" ? "/docs" : `/docs/${page}`}
                  className={page === slug ? "active" : ""}
                >
                  {label}
                </Link>
              ))}
            </div>
          ))}
        </aside>

        <main className="docs-main">
          <div className="docs-kicker">
            <BookOpen className="h-4 w-4" />
            Docs
            <ChevronRight className="h-3.5 w-3.5" />
            <span>{title}</span>
          </div>
          {doc.status === "loading" && <div className="docs-loading">Loading docs...</div>}
          {doc.status === "missing" && (
            <article className="docs-article">
              <h1>Page not found</h1>
              <p>The requested docs page is not available in this branch yet.</p>
            </article>
          )}
          {doc.status === "ready" && (
            <article className="docs-article">
              <h1>{title}</h1>
              {doc.meta.description && <p className="docs-description">{doc.meta.description}</p>}
              {renderMarkdown(doc.body)}
            </article>
          )}

          {nextDoc && (
            <Link to={`/docs/${nextDoc[0]}`} className="docs-next">
              <span>Next</span>
              <strong>{nextDoc[1]}</strong>
              <ChevronRight className="h-4 w-4" />
            </Link>
          )}
        </main>
      </div>
    </div>
  );
}
