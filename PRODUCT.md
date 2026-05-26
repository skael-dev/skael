# Product

## Register

product

## Users

Platform engineers, DevEx leads, and staff engineers at teams of 10 to 500 developers using AI coding agents. They maintain shared coding standards, review processes, and deployment checklists as SKILL.md files. They've hit the distribution wall: skills scattered across home directories, manually copied between agents, no visibility into what's actually used.

Secondary: solo developers organizing a personal skill library with versioning and search.

Context: desktop browser on a large monitor during working hours. The dashboard is a tool they check when publishing, syncing, or investigating skill usage, not something they live in all day. The CLI is the primary daily touchpoint.

## Product Purpose

Skael is the control plane for AI agent skills. It gives teams a central registry where skills are published with versioning and security scanning, a CLI that syncs skills to every developer's agent tools with one command, and a dashboard for exploration, search, and activation tracking across all agents.

Success: a team publishes skills once, every developer's agents get them automatically, and the team can see which skills are actually being used.

## Brand Personality

Precise, technical, confident.

Skael is infrastructure for people who build infrastructure. The interface should feel like it was made by someone who ships CLI tools and databases, not someone who makes marketing sites. Substance over style, but style is not absent: it's restrained and intentional.

Voice: direct, no filler. Copy reads like well-written docs, not marketing. Technical terms used without apology.

## Anti-references

- Generic SaaS (Intercom, HubSpot): rounded corners everywhere, illustration-heavy, pastel gradients, "friendly" in a way that feels patronizing to engineers.
- Over-designed dev tools (Vercel circa 2024): too much glass, too many gradients, blur effects as decoration, marketing spectacle where substance should lead.
- Enterprise gray (Jira, ServiceNow): dense, gray, dated. Functional but soul-crushing. Skael should feel alive, not like an admin panel from 2015.

Positive references: Linear (speed, density, keyboard-first), Raycast (monospace confidence, dark theme done right), Stripe Dashboard (information hierarchy, typography discipline).

## Design Principles

1. **Density earns trust.** Show data, not decoration. Engineers trust interfaces that respect their screen real estate. Empty space is intentional rhythm, not padding to fill a template.
2. **The CLI is the hero.** The dashboard complements the CLI, not the other way around. Terminal aesthetics (monospace numerals, command-line patterns) should echo through the UI.
3. **One way to do it.** Every action has one obvious path. No redundant navigation, no duplicate controls for the same function.
4. **Quiet until it matters.** Color and motion are reserved for state changes and warnings. Amber means something; green means something. Don't dilute signals with decoration.
5. **Self-hosted confidence.** The UI should feel like production software that runs on your infrastructure, not a SaaS trial. No upsell friction, no artificial limitations visible in the free tier.

## Accessibility & Inclusion

WCAG 2.1 AA compliance. Keyboard navigation for all interactive elements. Focus-visible indicators that match hover affordances. Sufficient contrast ratios on the dark theme (4.5:1 for text, 3:1 for interactive elements). Reduced motion support via `prefers-reduced-motion`.
