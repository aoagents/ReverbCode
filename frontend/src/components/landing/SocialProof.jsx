import React from "react";
import { motion } from "framer-motion";
import { ArrowUpRight, MessageCircle } from "lucide-react";

const posts = [
  {
    url: "https://twitter.com/Teknium/status/2042318941457170790",
    label: "Signal",
    author: "Teknium",
    note: "Most important outside validation.",
    featured: true,
  },
  {
    url: "https://twitter.com/facito0/status/2036380796475547760",
    label: "Mood",
    author: "FacitoO",
    note: "A lightweight social proof hit from daily AO usage.",
    featured: true,
  },
  {
    url: "https://twitter.com/buchireddy/status/2064108144607760628",
    label: "Builder",
    author: "Buchi Reddy B",
    note: "Went all-in early on the AO building blocks.",
  },
  {
    url: "https://twitter.com/oxwizzdom/status/2043491248376336484",
    label: "Code read",
    author: "oxwizzdom",
    note: "Weekend codebase teardown and minimal rebuild.",
  },
  {
    url: "https://twitter.com/addddiiie/status/2037174432700211408",
    label: "Use case",
    author: "Adi",
    note: "Parallel dev agents framed in one clean line.",
  },
  {
    url: "https://twitter.com/aoagents/status/2054207237548302804",
    label: "Official",
    author: "Agent Orchestrator",
    note: "A short official signal from the AO account.",
  },
];

const postColumns = [
  [posts[0], posts[2]],
  [posts[1], posts[3]],
  [posts[4], posts[5]],
];

function loadTwitterWidgets() {
  if (window.twttr?.widgets) return Promise.resolve(window.twttr);

  return new Promise((resolve) => {
    const existing = document.getElementById("twitter-wjs");
    if (existing) {
      existing.addEventListener("load", () => resolve(window.twttr), { once: true });
      return;
    }

    const script = document.createElement("script");
    script.id = "twitter-wjs";
    script.src = "https://platform.twitter.com/widgets.js";
    script.async = true;
    script.charset = "utf-8";
    script.onload = () => resolve(window.twttr);
    document.body.appendChild(script);
  });
}

function useTwitterEmbeds(theme) {
  React.useEffect(() => {
    let cancelled = false;
    loadTwitterWidgets().then((twttr) => {
      if (!cancelled) twttr?.widgets?.load?.();
    });
    return () => {
      cancelled = true;
    };
  }, [theme]);
}

function usePageTheme() {
  const [theme, setTheme] = React.useState(
    () => document.documentElement.dataset.theme || "dark"
  );

  React.useEffect(() => {
    const observer = new MutationObserver(() => {
      setTheme(document.documentElement.dataset.theme || "dark");
    });
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => observer.disconnect();
  }, []);

  return theme;
}

export default function SocialProof() {
  const theme = usePageTheme();
  useTwitterEmbeds(theme);

  return (
    <section
      id="testimonials"
      data-testid="social-proof"
      className="relative py-24 sm:py-32 border-t border-[color:var(--border)] overflow-hidden"
    >
      <div className="container-page">
        <div className="grid lg:grid-cols-12 gap-8 mb-12 items-end">
          <div className="lg:col-span-7">
            <div className="serial-num text-xs font-mono mb-3">06 - in the wild</div>
            <h2
              className="font-display font-bold tracking-tight leading-[1.02] text-[color:var(--fg)]"
              style={{ fontSize: "clamp(32px, 4.8vw, 60px)" }}
            >
              People are already{" "}
              <span className="font-editorial italic font-medium text-[color:var(--accent)]">
                building around it.
              </span>
            </h2>
          </div>
          <div className="lg:col-span-5">
            <p className="text-[15px] leading-relaxed text-[color:var(--fg-muted)]">
              Real posts from builders, researchers, and early users, embedded directly from X.
            </p>
          </div>
        </div>

        <div className="grid md:grid-cols-2 xl:grid-cols-3 gap-4 items-start">
          {postColumns.map((column, columnIndex) => (
            <div key={columnIndex} className="flex flex-col gap-4">
              {column.map((post, postIndex) => (
                <TweetCard
                  key={post.url}
                  post={post}
                  index={columnIndex * 2 + postIndex}
                  theme={theme}
                />
              ))}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function TweetCard({ post, index, theme }) {
  return (
    <motion.article
      initial={{ opacity: 0, y: 18 }}
      whileInView={{ opacity: 1, y: 0 }}
      viewport={{ once: true, margin: "-80px" }}
      transition={{ duration: 0.45, delay: index * 0.05 }}
      data-testid={`tweet-card-${index}`}
      className="surface overflow-hidden"
    >
      <div className="flex items-center justify-between gap-3 border-b border-[color:var(--border)] bg-[color:var(--bg-chrome)] px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <MessageCircle className="h-4 w-4 shrink-0 text-[color:var(--accent)]" />
          <div className="min-w-0">
            <div className="font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--fg-dim)]">
              {post.label}
            </div>
            <div className="truncate text-[13px] font-semibold text-[color:var(--fg)]">
              {post.author}
            </div>
          </div>
        </div>
        <a
          href={post.url}
          target="_blank"
          rel="noreferrer"
          aria-label={`Open ${post.author} post`}
          className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-[color:var(--border-strong)] text-[color:var(--fg-muted)] hover:text-[color:var(--accent)]"
        >
          <ArrowUpRight className="h-4 w-4" />
        </a>
      </div>

      <div className="px-3 pb-4 pt-3">
        <p className="mb-3 px-1 text-[13px] leading-relaxed text-[color:var(--fg-muted)]">
          {post.note}
        </p>
        <div className="tweet-shell [&_.twitter-tweet]:mx-auto [&_.twitter-tweet]:max-w-full">
          <blockquote
            className="twitter-tweet"
            data-theme={theme === "light" ? "light" : "dark"}
            data-dnt="true"
            data-conversation="none"
          >
            <a href={post.url}>View post on X</a>
          </blockquote>
        </div>
      </div>
    </motion.article>
  );
}
