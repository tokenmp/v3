# syntax=docker/dockerfile:1
# Runs the pre-built Next.js standalone server.
# Build context: repo root (so we can copy .next/standalone + static from apps/web).
FROM node:22-alpine AS runner

WORKDIR /app

ENV NODE_ENV=production
ENV PORT=3100
ENV HOSTNAME=0.0.0.0
# Mock auth enabled by default (no backend on dev server).
ENV NEXT_PUBLIC_USE_MOCK_AUTH=1

# Copy the standalone server (includes its own node_modules).
COPY apps/web/.next/standalone ./

# Copy static assets and public (not traced by standalone).
COPY apps/web/.next/static ./apps/web/.next/static
COPY apps/web/public ./apps/web/public

EXPOSE 3100

CMD ["node", "apps/web/server.js"]
