# site/ — Quartz configuration overlay

This directory holds **only the configuration** for the [Quartz](https://quartz.jzhao.xyz)
rendering of the vault that the service serves at `/`. It is **not** a full Quartz project.

Quartz is a clone-and-customize static site generator, not an npm dependency: a build overlays the
two files here onto a pinned upstream Quartz checkout, stages the vault content (`index.md` +
`library/`), and runs `quartz build`; the output is published outside the vault and bind-mounted
into the service container at `SITE_ROOT` (`/site`).

> **The build pipeline is being reworked** — the host-side builder (`vault-site.sh`, then a
> `knowledge-tools site` command) has been retired while two directions are evaluated: a live
> render inside the service image, or a standalone Quartz-backed renderer in its own image. These
> config files are the starting point for whichever lands; until then, build with your own tooling.

- `quartz.config.ts` — site config: title/baseUrl from env, no analytics, Obsidian wikilinks
  enabled, private dirs in `ignorePatterns` (belt-and-suspenders on top of the staging allowlist).
- `quartz.layout.ts` — page layout (close to the Quartz default, minimal footer).

## Why these files don't typecheck in this repo

Both files `import` from `./quartz/cfg` and `./quartz/components` (and use `process.env`). Those
resolve only inside the Quartz checkout, where the files are copied at build time — not here. The
TypeScript errors you'll see when opening them in this repo are expected; the service's own `tsc`
build (`service/src/**` only) never compiles them.

## Pinned version

These overlays target **Quartz v4** (`v4.5.2`). Quartz v5 changed the configuration model (YAML)
and is a deliberate future upgrade, not a drop-in — moving to it requires porting these two files.
Whatever the reworked build pipeline pins to should match the version these overlays target.

## Local preview

From a checkout that has these files overlaid, you can use Quartz's dev server:

```sh
npx quartz build --serve -d /path/to/staged/content
```
