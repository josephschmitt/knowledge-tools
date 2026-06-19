import { QuartzConfig } from "./quartz/cfg"
import * as Plugin from "./quartz/plugins"

/**
 * knowledge-tools Quartz configuration — the rendering of the vault served at / by the service.
 *
 * This file is an OVERLAY: it is copied into a pinned upstream Quartz checkout by
 * scripts/vault-site.sh before `quartz build` runs, which is why the imports below resolve against
 * `./quartz/*` (that tree only exists inside the checkout, not in this repo). See site/README.md.
 *
 * It is plain TypeScript executed by Node at build time, so it reads a couple of knobs from the
 * environment (set by vault-site.sh / the per-vault env file):
 *   - KNOWLEDGE_SITE_TITLE     page title           (default "Knowledge Vault")
 *   - KNOWLEDGE_SITE_BASE_URL  host for RSS/sitemap/404 absolute URLs, WITHOUT scheme (e.g.
 *                              "wiki.example.com"). Defaults to "example.com" — Quartz navigation
 *                              is relative, so this only affects absolute URLs in RSS/sitemap/404.
 *                              (An empty value breaks Quartz's 404 emitter, so we never pass "".)
 */
const config: QuartzConfig = {
  configuration: {
    pageTitle: process.env.KNOWLEDGE_SITE_TITLE ?? "Knowledge Vault",
    pageTitleSuffix: "",
    enableSPA: true,
    enablePopovers: true,
    // No third-party analytics on a personal, auth-gated wiki.
    analytics: null,
    locale: "en-US",
    baseUrl: process.env.KNOWLEDGE_SITE_BASE_URL || "example.com",
    // The staging step in vault-site.sh is the real privacy boundary (it copies only index.md +
    // wiki/). These patterns are belt-and-suspenders so nothing private renders even if staging
    // ever changes; on top of Quartz's own defaults.
    ignorePatterns: [
      "private",
      "templates",
      ".obsidian",
      "inbox",
      "outputs",
      "tasks",
      ".review",
      ".compile",
    ],
    defaultDateType: "modified",
    theme: {
      fontOrigin: "googleFonts",
      cdnCaching: true,
      typography: {
        header: "Schibsted Grotesk",
        body: "Source Sans Pro",
        code: "IBM Plex Mono",
      },
      colors: {
        lightMode: {
          light: "#faf8f8",
          lightgray: "#e5e5e5",
          gray: "#b8b8b8",
          darkgray: "#4e4e4e",
          dark: "#2b2b2b",
          secondary: "#284b63",
          tertiary: "#84a59d",
          highlight: "rgba(143, 159, 169, 0.15)",
          textHighlight: "#fff23688",
        },
        darkMode: {
          light: "#161618",
          lightgray: "#393639",
          gray: "#646464",
          darkgray: "#d4d4d4",
          dark: "#ebebec",
          secondary: "#7b97aa",
          tertiary: "#84a59d",
          highlight: "rgba(143, 159, 169, 0.15)",
          textHighlight: "#b3aa0288",
        },
      },
    },
  },
  plugins: {
    transformers: [
      Plugin.FrontMatter(),
      Plugin.CreatedModifiedDate({
        priority: ["frontmatter", "git", "filesystem"],
      }),
      Plugin.SyntaxHighlighting({
        theme: {
          light: "github-light",
          dark: "github-dark",
        },
        keepBackground: false,
      }),
      // Obsidian-flavored markdown + link crawling resolve the vault's [[wikilinks]] (the notes
      // have no frontmatter and rely on wikilinks for cross-references) — keep both enabled.
      Plugin.ObsidianFlavoredMarkdown({ enableInHtmlEmbed: false }),
      Plugin.GitHubFlavoredMarkdown(),
      Plugin.TableOfContents(),
      Plugin.CrawlLinks({ markdownLinkResolution: "shortest" }),
      Plugin.Description(),
      Plugin.Latex({ renderEngine: "katex" }),
    ],
    filters: [Plugin.RemoveDrafts()],
    emitters: [
      Plugin.AliasRedirects(),
      Plugin.ComponentResources(),
      Plugin.ContentPage(),
      Plugin.FolderPage(),
      Plugin.TagPage(),
      Plugin.ContentIndex({
        enableSiteMap: true,
        enableRSS: true,
      }),
      Plugin.Assets(),
      Plugin.Static(),
      Plugin.Favicon(),
      Plugin.NotFoundPage(),
      // CustomOgImages() is omitted: it pulls heavy rendering deps and slows the build, and the
      // site is auth-gated so social-card previews aren't useful. Re-add it if you want OG images.
    ],
  },
}

export default config
