import React, { useState } from "react";
import { motion } from "framer-motion";
import { Play } from "lucide-react";

const VIDEO_ID = "QdwaeEXOmDs";
const VIDEO_TITLE = "Agent Orchestrator Launch Demo";

export default function VideoSection() {
  const [isPlaying, setIsPlaying] = useState(false);

  return (
    <section
      id="see-it"
      data-testid="video-section"
      className="relative py-24 sm:py-32 border-t border-[color:var(--border)]"
    >
      <div className="container-page">
        <div className="text-center mb-10">
          <div className="serial-num text-xs font-mono mb-3 opacity-70">
            see it in action
          </div>
          <h2
            className="font-display font-bold tracking-tight leading-[1.02] text-[color:var(--fg)] inline-block"
            style={{ fontSize: "clamp(28px, 3.6vw, 44px)" }}
          >
            Watch the founder walk through it -{" "}
            <span className="font-editorial italic font-medium text-[color:var(--accent)]">
              100 PRs in 6 days.
            </span>
          </h2>
        </div>

        <motion.div
          initial={{ opacity: 0, y: 16 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true, margin: "-100px" }}
          transition={{ duration: 0.6 }}
          className="relative max-w-5xl mx-auto"
        >
          <div className="absolute -inset-3 bg-[color:var(--accent)] opacity-[0.045] blur-2xl rounded-3xl pointer-events-none" />
          <div
            data-testid="video-frame"
            className="relative aspect-video rounded-2xl overflow-hidden border border-[color:var(--border-strong)] glow-accent bg-black"
          >
            {isPlaying ? (
              <iframe
                data-testid="video-iframe"
                className="absolute inset-0 h-full w-full"
                src={`https://www.youtube-nocookie.com/embed/${VIDEO_ID}?autoplay=1&rel=0&modestbranding=1&playsinline=1`}
                title={VIDEO_TITLE}
                frameBorder="0"
                allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                referrerPolicy="strict-origin-when-cross-origin"
                allowFullScreen
              />
            ) : (
              <button
                type="button"
                data-testid="video-preview"
                onClick={() => setIsPlaying(true)}
                className="absolute inset-0 group block w-full h-full"
                aria-label={`Play ${VIDEO_TITLE}`}
              >
                <img
                  src={`https://i.ytimg.com/vi/${VIDEO_ID}/maxresdefault.jpg`}
                  alt={VIDEO_TITLE}
                  className="h-full w-full object-cover"
                />
                <span className="absolute inset-0 bg-black/10 group-hover:bg-black/0 transition-colors" />
                <span className="absolute left-1/2 top-1/2 grid h-16 w-16 -translate-x-1/2 -translate-y-1/2 place-items-center rounded-2xl bg-[#ff0033] text-white shadow-lg transition-transform group-hover:scale-105">
                  <Play className="h-7 w-7 translate-x-0.5 fill-current" />
                </span>
              </button>
            )}
          </div>
        </motion.div>
      </div>
    </section>
  );
}
