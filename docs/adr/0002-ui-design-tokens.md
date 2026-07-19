# ADR 0002: UI Design Tokens

- Status: accepted
- Date: 2026-07-20

## Context

TokenMP plans separate Web and Admin applications that need a shared visual language without importing each other's private source code. The current phase intentionally creates no applications or React components, but the token contract must be reviewable and consumable before either application starts.

The previous TokenMP repository demonstrated CSS custom properties, OKLCH colors, Tailwind CSS v4 mapping, shadcn-compatible semantic names, and Light/Dark themes. It also coupled Admin styling directly to User frontend source and allowed many raw status colors and arbitrary values in pages. TokenMP v3 needs a stable Monorepo package boundary rather than source aliases between applications.

## Decision

- Create `@tokenmp/ui-tokens` as a CSS-only workspace package.
- Use CSS custom properties with the `--tmp-*` prefix as the runtime contract.
- Use OKLCH for reference color definitions.
- Separate reference tokens from semantic tokens; applications and components consume semantic tokens.
- Support explicit `data-theme="light"`, `data-theme="dark"`, the `.dark` compatibility class, and system dark preference when no explicit theme is selected.
- Preserve the previous TokenMP primitive color values, core Light/Dark semantic mappings, radius scale, shadows, motion timing, layer values, and font stacks as the v3 visual compatibility baseline.
- Keep an Industrial / Utilitarian visual direction with neutral surfaces, restrained elevation, explicit boundaries, and high-legibility status colors.
- Keep the previous `PingFang SC` and platform fallback font stacks without distributing font assets; a Web Font change requires a separate visual decision.
- Export the framework-neutral core at `@tokenmp/ui-tokens`.
- Export pre-defined Tailwind CSS v4 and shadcn compatibility integrations at `@tokenmp/ui-tokens/tailwind` and `@tokenmp/ui-tokens/shadcn`.
- Keep integration files as aliases only; Tailwind, shadcn, font loading, and theme state remain consumer responsibilities.
- Use dependency-free Node.js scripts for CSS contract validation and distribution builds.
- Do not create apps, React components, Storybook, or a UI gallery in this decision's implementation.

## Consequences

- Future Web and Admin applications can share a public CSS contract without importing each other's source.
- Initial v3 screens can retain the previous product's visual baseline while using stricter package and semantic boundaries.
- Core tokens remain usable without Tailwind or shadcn.
- The two integration exports are public experimental contracts before real application integration; the first consumers must verify Tailwind compilation and browser behavior.
- Theme values can change without changing semantic names, but deleting or changing the meaning of a semantic token is a breaking contract change.
- The package does not prove font delivery, WCAG contrast, responsive application behavior, or visual regression by itself. Those checks become mandatory when the first application consumes it.
- Component-specific tokens remain intentionally limited until shared components establish real requirements.

## Alternatives Considered

- Keep tokens inside the first application: rejected because Web and Admin are confirmed future consumers and direct app-to-app source sharing is prohibited.
- Export only framework-neutral CSS and define integrations later: viable and simpler, but rejected after deciding to establish Tailwind v4 and shadcn mappings as part of the initial reviewed contract.
- Redesign all primitive values and typography for v3: rejected because the first goal is a governed migration of the existing visual baseline, not an unrequested visual rebrand.
- Use Tailwind defaults as the source of truth: rejected because runtime theming and framework independence require a product-owned semantic contract.
- Use shadcn variable names as the core contract: rejected because unprefixed names are compatibility conventions and can collide with other libraries.
- Introduce Style Dictionary or DTCG JSON now: deferred because this phase has one CSS platform and no Figma or multi-platform generation pipeline. It can be reconsidered when another output format becomes a real consumer.
- Build a shared React UI package now: rejected because components and applications are explicitly outside this phase.
