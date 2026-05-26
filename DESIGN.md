---
name: Skael
description: The control plane for AI agent skills
colors:
  forge-amber: "#f59e0b"
  forge-amber-muted: "#92400e"
  forge-amber-surface: "rgba(245, 158, 11, 0.08)"
  void-black: "#0a0a0a"
  surface-primary: "#141414"
  surface-raised: "#1e1e1e"
  wire-dim: "#262626"
  wire-active: "#404040"
  ash-light: "#ededed"
  ash-mid: "#a0a0a0"
  ash-deep: "#666666"
  signal-danger: "#ef4444"
  signal-info: "#3b82f6"
  chart-blue: "#3b82f6"
  chart-purple: "#8b5cf6"
  chart-red: "#ef4444"
typography:
  display:
    fontFamily: "'Geist', system-ui, sans-serif"
    fontSize: "clamp(2rem, 4vw, 3rem)"
    fontWeight: 500
    lineHeight: 1.1
    letterSpacing: "-0.025em"
  title:
    fontFamily: "'Geist', system-ui, sans-serif"
    fontSize: "18px"
    fontWeight: 600
    lineHeight: 1.25
    letterSpacing: "-0.015em"
  body:
    fontFamily: "'Geist', system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.55
  label:
    fontFamily: "'Geist Mono', 'SF Mono', monospace"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: "0.02em"
  mono:
    fontFamily: "'Geist Mono', 'SF Mono', monospace"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.6
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
  pill: "9999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "16px"
  lg: "24px"
  xl: "32px"
components:
  button-primary:
    backgroundColor: "{colors.ash-light}"
    textColor: "{colors.void-black}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
  button-primary-hover:
    backgroundColor: "rgba(237, 237, 237, 0.9)"
  button-secondary:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.ash-light}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.ash-mid}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
  button-ghost-hover:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.ash-light}"
  badge-default:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.ash-mid}"
    rounded: "{rounded.pill}"
    padding: "2px 8px"
  badge-accent:
    backgroundColor: "{colors.forge-amber-surface}"
    textColor: "{colors.forge-amber}"
    rounded: "{rounded.pill}"
    padding: "2px 8px"
  input-default:
    backgroundColor: "{colors.surface-primary}"
    textColor: "{colors.ash-light}"
    rounded: "{rounded.md}"
    padding: "8px 12px"
---

# Design System: Skael

## 1. Overview

**Creative North Star: "The Forge"**

Skael's interface is a forge: where skills are shaped, tested, and tempered before they reach production agents. Dark surfaces absorb attention into the content. Amber heat marks what's active, what's changing, what demands a decision. Everything else recedes into carbon-black stillness.

The system rejects decoration. No illustrations, no gradient flourishes, no friendly rounded cards with pastel backgrounds. It rejects the enterprise-gray trap equally: Skael is alive, not administrative. The difference is signal. Amber glows when something fires. Red appears when something breaks. The rest is monochrome discipline.

Density earns trust with this audience. A platform engineer scanning 40 skills doesn't want whitespace; they want data. The UI borrows from the CLI it complements: monospace numerals, terminal-echo patterns, compact rows that reward scanning.

**Key Characteristics:**
- Dark-only, amber accent, monochrome neutrals
- Flat surfaces separated by 1px borders, not shadows
- Monospace for data, sans-serif for prose
- Motion only on state changes (fade-up entrances, glow pulses)
- CLI-native aesthetics adapted to spatial layout

## 2. Colors: The Forge Palette

A restrained palette: tinted neutrals plus one accent held below 10% of any surface. Amber is the forge's heat; its rarity is the point.

### Primary
- **Forge Amber** (#f59e0b): The single accent. Active states, indicators, accent glows, CTAs on the landing page. Used sparingly in the product register; more liberally in brand surfaces (landing page hero, CTAs). Never as a background fill larger than a button.
- **Forge Amber Muted** (#92400e): Darkened amber for subtle borders on accent elements, hover states on amber badges.
- **Forge Amber Surface** (rgba(245, 158, 11, 0.08)): Translucent amber wash for accent-tinted containers, ambient glows, hover backgrounds on accent-adjacent elements.

### Neutral
- **Void Black** (#0a0a0a): Primary background. The canvas. Not pure black; faintly warm.
- **Surface Primary** (#141414): Cards, popovers, sidebar background. One step above the void.
- **Surface Raised** (#1e1e1e): Tertiary surfaces, input backgrounds, secondary buttons. The "shelf" level.
- **Wire Dim** (#262626): Default borders, dividers, input strokes. The structural grid.
- **Wire Active** (#404040): Active borders, focus rings. The grid responding to interaction.
- **Ash Light** (#ededed): Primary text. Not white; slightly warm. High contrast against void.
- **Ash Mid** (#a0a0a0): Secondary text. Labels, descriptions, inactive nav items.
- **Ash Deep** (#666666): Tertiary text. Timestamps, captions, disabled states. Use with caution: below WCAG AA on void-black for body text. Acceptable only for decorative or non-essential labels.

### Signal
- **Danger** (#ef4444): Errors, destructive actions, critical scan findings.
- **Info** (#3b82f6): Informational states, secondary chart series.

### Named Rules
**The Forge Heat Rule.** Amber appears on no more than 10% of any product screen. Its rarity is its power. On brand surfaces (landing page), amber may carry up to 30% as a hero accent.

**The No Status Green Rule.** Green was removed as the accent to avoid "generic dev tool" association. If a future feature needs a "success" color, use a muted teal or simply remove the error state. Do not reintroduce #22c55e as an accent.

## 3. Typography

**Display Font:** Geist (with system-ui, sans-serif fallback)
**Body Font:** Geist (with system-ui, sans-serif fallback)
**Label/Mono Font:** Geist Mono (with SF Mono, monospace fallback)

**Character:** Geist's geometric precision pairs with its monospace sibling for a system that reads like well-typeset technical documentation. The mono variant carries data, versions, skill names, and CLI output. The sans variant carries prose, descriptions, and navigation.

### Hierarchy
- **Display** (500, clamp(2rem, 4vw, 3rem), 1.1): Page titles on the landing page. Not used in the dashboard.
- **Title** (600, 18px, 1.25, -0.015em tracking): Section headers, skill names in detail view, sidebar section labels.
- **Body** (400, 14px, 1.55): Descriptions, markdown content, form labels. Max line length 65-75ch.
- **Label** (Mono 500, 12px, 1.4, 0.02em tracking): Uppercase section markers, metadata labels, stat captions.
- **Mono** (Mono 400, 13px, 1.6): Skill names in lists, version numbers, terminal output, code blocks, tabular data. Use `font-variant-numeric: tabular-nums` for numeric columns.

### Named Rules
**The Mono-for-Data Rule.** Any value that is a skill name, version number, checksum, count, percentage, or timestamp uses Geist Mono. Sans-serif is for sentences; monospace is for facts.

## 4. Elevation

Flat. No box-shadows on cards or containers at rest. Depth is conveyed through tonal layering (void → surface-primary → surface-raised) and 1px borders (wire-dim).

The only shadows are **accent glows**: `0 0 8px var(--color-accent)` or `0 0 12px var(--color-accent-surface)` on active indicators and focused accent elements. These are signal, not decoration.

### Named Rules
**The No Shadow Rule.** Surfaces are flat. If you reach for `box-shadow` on a card, container, or panel, use a 1px border instead. Shadows exist only as colored glows on active/focused accent elements.

## 5. Components

### Buttons
- **Shape:** Gently rounded (6px radius)
- **Primary:** Ash Light (#ededed) background, Void Black text. Compact: h-9 (36px), px-4. The only high-contrast element on the page.
- **Hover:** 90% opacity background. No color shift.
- **Focus:** 3px ring in Wire Active with 50% opacity.
- **Secondary:** Surface Raised background, Ash Light text. Same dimensions.
- **Ghost:** Transparent background, Ash Mid text. Hover reveals Surface Raised.
- **Destructive:** Danger red background, white text. Reserved for delete actions.

### Badges / Pills
- **Style:** Pill-shaped (9999px radius), compact (py-0.5, px-2), text-xs.
- **Default:** Surface Raised background, Ash Mid text. For version numbers, counts.
- **Accent:** Amber Surface background, Forge Amber text. For active indicators.
- **Outline:** Wire Dim border, no fill. For status pills.
- **Tag colors:** Fixed palette per tag name (review=purple-400, deploy=emerald-400, security=red-400, testing=blue-400, api=amber-400, db=cyan-400). Dot indicators only, not full background fills.

### Inputs / Fields
- **Style:** Surface Primary background, Wire Dim border, 6px radius. 14px text.
- **Focus:** Border shifts to Wire Active. 3px ring in Wire Active at 50% opacity.
- **Search inputs:** Compact (h-8), monospace placeholder text, magnifying glass icon in Ash Deep.

### Navigation
- **Sidebar:** Surface Primary background, full height. Section headers in Label style (mono, uppercase, 0.08em tracking, Ash Deep). Items: 14px Geist, Ash Mid default, Ash Light on active with Surface Raised background. 5px left-radius highlight on active item.
- **Top bar:** Void Black background with Wire Dim bottom border. Breadcrumb + status indicator (amber dot with glow for "connected" state).

### Stat Cells
- **Layout:** Vertical stack: label (Label style, Ash Deep) above value (Mono 13px, Ash Light, tabular-nums).
- **Trend indicators:** Inline with value. Green (#22c55e) for up, Danger for down. Monospace, 12px. These are the only place green appears in the product.

### Terminal Blocks (signature component)
- **Chrome bar:** Surface Primary background, Wire Dim bottom border, 36px height. Three dots (Wire Dim circles, 11px). Title in Mono 12px, Ash Deep.
- **Body:** Void Black to Surface Primary gradient background. Mono 13px, 1.75 line-height. Prompt character in Forge Amber. Output in Ash Mid. Success markers in green. Blinking cursor on last line.

## 6. Do's and Don'ts

### Do:
- **Do** use monospace for every piece of data: skill names, versions, checksums, counts, timestamps. Sans-serif is for prose only.
- **Do** separate surfaces with 1px solid Wire Dim borders. Borders are the structural grid.
- **Do** use amber glows (`box-shadow: 0 0 Npx var(--color-accent)`) for active indicators and focused accent elements only.
- **Do** test all text against WCAG AA on Void Black. Ash Deep (#666666) fails for body text; restrict it to non-essential labels.
- **Do** match `:focus-visible` treatments to hover treatments. If hover gets a background, focus gets the same background plus a ring.
- **Do** use `font-variant-numeric: tabular-nums` on numeric columns so numbers align vertically.

### Don't:
- **Don't** use `border-left` or `border-right` greater than 1px as a colored accent stripe on cards, list items, or callouts. Use full borders, background tints, or leading icons instead.
- **Don't** apply `background-clip: text` with a gradient. Use a single solid color for emphasis.
- **Don't** use blur/glass effects decoratively. If backdrop-filter appears, it must serve a functional purpose (e.g., sticky nav over scrolling content).
- **Don't** build hero-metric templates (big number, small label, gradient accent). This is SaaS cliche. Stat cells use the compact vertical stack format.
- **Don't** create identical card grids. Avoid icon + heading + text repeated in uniform boxes. Use varied density and layout.
- **Don't** default to modals. Use inline expansion, drawers, or progressive disclosure first.
- **Don't** use box-shadow on cards or containers. The system is flat. Borders, not shadows.
- **Don't** use pastel backgrounds, illustrations, or "friendly" rounded-everything aesthetics. This is infrastructure tooling, not consumer SaaS. (Anti-reference: Intercom, HubSpot.)
- **Don't** overdesign with gradients, glass, and blur effects for spectacle. Substance leads. (Anti-reference: Vercel circa 2024.)
- **Don't** let the interface feel gray, dense, and dated. Skael is alive, not an admin panel. (Anti-reference: Jira, ServiceNow.)
- **Don't** use green (#22c55e) as the primary accent. It was removed to avoid "generic dev tool" association. Green appears only in trend-up indicators and terminal success markers.
