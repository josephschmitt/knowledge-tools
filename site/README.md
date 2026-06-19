# site/ — Quartz configuration overlay

This directory holds **only the configuration** for the [Quartz](https://quartz.jzhao.xyz)
rendering of the vault that the service serves at `/`. It is **not** a full Quartz project.

Quartz is a clone-and-customize static site generator, not an npm dependency. So
`scripts/vault-site.sh` maintains a pinned upstream Quartz checkout in a host state dir
(`~/.local/state/knowledge-tools/quartz` by default, pinned to `KNOWLEDGE_QUARTZ_REF`), copies the
two files here on top of it, stages the vault content, and runs `quartz build`. The build output
is published outside the vault and bind-mounted into the service container at `SITE_ROOT` (`/site`).

- `quartz.config.ts` — site config: title/baseUrl from env, no analytics, Obsidian wikilinks
  enabled, private dirs in `ignorePatterns` (belt-and-suspenders on top of the staging allowlist).
- `quartz.layout.ts` — page layout (close to the Quartz default, minimal footer).

## Why these files don't typecheck in this repo

Both files `import` from `./quartz/cfg` and `./quartz/components` (and use `process.env`). Those
resolve only inside the Quartz checkout, where the files are copied at build time — not here. The
TypeScript errors you'll see when opening them in this repo are expected; the service's own `tsc`
build (`service/src/**` only) never compiles them.

## Pinned version

Pinned to **Quartz v4** (`v4.5.2`) via `KNOWLEDGE_QUARTZ_REF`. Quartz v5 changed the configuration
model (YAML) and is a deliberate future upgrade, not a drop-in — bumping the pin requires porting
these two files. To change the pin, set `KNOWLEDGE_QUARTZ_REF` and update these overlays to match.

## Local preview

From a checkout that has these files overlaid, you can use Quartz's dev server:

```sh
npx quartz build --serve -d /path/to/staged/content
```
