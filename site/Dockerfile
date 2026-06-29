# Self-contained knowledge-site image: builds the vault's Quartz site INSIDE the container and
# serves it on its own URL. Quartz is a clone-and-customize generator (NOT an npm dep), so we bake
# a pinned upstream checkout + this repo's config overlay + node_modules into the image at build
# time (the heavy, cacheable layer). At runtime the container only stages the bind-mounted vault's
# public content (index.md + library/) and runs `quartz build`, then serves the output — and
# rebuilds on an authenticated POST /rebuild. See site/README.md.
FROM node:24-slim

# Bump the ref here (and keep site/quartz.{config,layout}.ts in sync — see site/README.md). Pinned
# to Quartz v4; v5 is a port, not a bump.
ARG KNOWLEDGE_QUARTZ_REF=v4.5.2
ARG KNOWLEDGE_QUARTZ_URL=https://github.com/jackyzha0/quartz

# git: clone Quartz at build time. rsync: the --delete staging delta in build.sh. ca-certificates:
# HTTPS clone. bash: build.sh/entrypoint.sh use arrays.
RUN apt-get update \
 && apt-get install -y --no-install-recommends git rsync ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# Bake the pinned Quartz checkout + its config overlay + deps. Imports in the overlay resolve
# against ./quartz/* — which only exists inside this checkout.
RUN git clone --quiet --depth 1 --branch "$KNOWLEDGE_QUARTZ_REF" "$KNOWLEDGE_QUARTZ_URL" /opt/quartz
WORKDIR /opt/quartz
COPY quartz.config.ts quartz.layout.ts ./
RUN npm ci

# The site scripts (build recipe + server).
COPY build.sh entrypoint.sh serve.mjs /opt/site/
RUN chmod +x /opt/site/build.sh /opt/site/entrypoint.sh \
 # /srv holds the staged content + published output; quartz writes a .quartz-cache into the
 # checkout at build time — both must be writable by the unprivileged runtime user.
 && mkdir -p /srv \
 && chown -R node:node /srv /opt/quartz

# uid 1000, matching the vault file owner on the host (same as the service image).
USER node
ENV VAULT_ROOT=/vault \
    KNOWLEDGE_SITE_PORT=8080
EXPOSE 8080
WORKDIR /opt/site
ENTRYPOINT ["/opt/site/entrypoint.sh"]
