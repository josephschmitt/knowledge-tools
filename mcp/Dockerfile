# Multi-stage build for the knowledge-vault MCP server.
# node:sqlite is built into Node 24 (no native module), so a slim base is enough.
FROM node:24-slim AS build
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci
COPY tsconfig.json ./
COPY src ./src
RUN npm run build

FROM node:24-slim AS runtime
WORKDIR /app
ENV NODE_ENV=production
COPY package.json package-lock.json* ./
RUN npm ci --omit=dev && npm cache clean --force
COPY --from=build /app/dist ./dist
# The `node` user is uid 1000, matching the vault file owner on the host.
USER node
EXPOSE 3000
CMD ["node", "dist/index.js"]
