# syntax=docker/dockerfile:1
# Artifact-only compatibility image. It requires a separately produced
# apps/web/.next output and is intentionally NOT the Compose/default deploy
# path; use Dockerfile.web for a clean-checkout production build.
# Build context: repo root.
FROM node:26.4.0-alpine AS runner

WORKDIR /app

ENV NODE_ENV=production
ENV PORT=3100
ENV HOSTNAME=0.0.0.0
# Public Next variables are already fixed in the supplied artifact.

# Copy the standalone server (includes its own node_modules).
COPY apps/web/.next/standalone ./

# public/ is optional and is not required by this app; never COPY it from a
# deployment checkout because it may not exist.
COPY apps/web/.next/static ./apps/web/.next/static

USER node
EXPOSE 3100

CMD ["node", "apps/web/server.js"]
