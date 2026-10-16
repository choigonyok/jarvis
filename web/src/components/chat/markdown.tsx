"use client";

import { memo } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

/**
 * The agent's reply, rendered as the document it already is.
 *
 * Every element here is bound by the same rules as the rest of the console:
 * the only bordered thing in this product is an approval card, so nothing
 * below draws a box; monospace is reserved for literal code and commands, so
 * it appears on `code` and nowhere else; and the measure stays at 36rem so a
 * line never runs past what is comfortable to read.
 */
export const Markdown = memo(function Markdown({ children }: { children: string }) {
  return (
    <div className="max-w-[36rem] space-y-2.5 text-[14.5px] leading-[1.75] text-pretty text-foreground/90">
      <ReactMarkdown remarkPlugins={PLUGINS} components={COMPONENTS}>
        {children}
      </ReactMarkdown>
    </div>
  );
});

// Module-level, not written inline: a new object (and new functions) on every
// render made React rebuild every element of every message whenever the thread
// re-rendered - a cost on a long thread, and it broke anything that holds on
// to the text (search marks).
const PLUGINS = [remarkGfm];

const COMPONENTS: Components = {
  p: ({ children }) => <p>{children}</p>,

  // Headings step down in weight, not in a different typeface. 600 is
  // the heaviest weight this console loads.
  h1: ({ children }) => (
    <h2 className="pt-1.5 text-[16px] font-semibold text-foreground">{children}</h2>
  ),
  h2: ({ children }) => (
    <h3 className="pt-1.5 text-[15px] font-semibold text-foreground">{children}</h3>
  ),
  h3: ({ children }) => (
    <h4 className="pt-1 text-[14.5px] font-medium text-foreground">{children}</h4>
  ),

  ul: ({ children }) => (
    <ul className="space-y-1 ps-[1.1em] [&>li]:list-disc">{children}</ul>
  ),
  ol: ({ children }) => (
    <ol className="space-y-1 ps-[1.3em] [&>li]:list-decimal">{children}</ol>
  ),
  // The marker is structure, not content: it stays quieter than the
  // words it introduces.
  li: ({ children }) => <li className="marker:text-faint">{children}</li>,

  a: ({ href, children }) => (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="underline decoration-dim underline-offset-[3px] transition-colors hover:decoration-foreground focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
    >
      {children}
    </a>
  ),

  strong: ({ children }) => (
    <strong className="font-semibold text-foreground">{children}</strong>
  ),
  em: ({ children }) => <em className="italic">{children}</em>,

  // Mono is the mark of something literal. It is the one place this
  // console changes typeface, which is why it must not spread.
  code: ({ children, className }) => {
    const block = /language-/.test(className ?? "");
    if (!block) {
      return (
        <code className="rounded-sm bg-glass-raised px-[0.4em] py-[0.15em] font-mono text-[12.5px] text-foreground">
          {children}
        </code>
      );
    }
    return <code className="font-mono text-[12.5px]">{children}</code>;
  },
  // Recessed rather than outlined: a block of code is something you
  // look into, and the well is the layer this console already has
  // for that.
  pre: ({ children }) => (
    <pre className="overflow-x-auto rounded-md bg-well p-3 leading-[1.6] text-foreground/90">
      {children}
    </pre>
  ),

  // A quote is marked by where it sits, not by a box around it.
  blockquote: ({ children }) => (
    <blockquote className="border-s border-edge ps-3 text-dim">{children}</blockquote>
  ),

  hr: () => <hr className="border-edge-soft" />,

  // An image is never loaded: a URL a reply carries - one a page the model
  // read may have put there - would be fetched the moment the reply showed,
  // with no click, and could carry whatever its query holds out. It shows
  // as a link, opened only if tapped. (The CSP's img-src says the same.)
  img: ({ src, alt }) =>
    typeof src === "string" && src ? (
      <a
        href={src}
        target="_blank"
        rel="noopener noreferrer"
        className="underline decoration-dim underline-offset-[3px] transition-colors hover:decoration-foreground focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
      >
        {alt || "이미지 링크"}
      </a>
    ) : null,

  // Tables get hairlines between rows and nothing around them. Wide
  // ones scroll inside their own box so the thread never does.
  table: ({ children }) => (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-[13.5px]">{children}</table>
    </div>
  ),
  th: ({ children }) => (
    <th className="border-b border-edge py-1.5 pe-3 text-start font-medium text-dim">
      {children}
    </th>
  ),
  td: ({ children }) => (
    <td className="border-b border-edge-soft py-1.5 pe-3 align-top">{children}</td>
  ),
};
