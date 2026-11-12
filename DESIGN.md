# Design System — Token Panosu

## Aesthetic Direction
Token Panosu blends **calm data journalism** with a **tactile isometric studio**. Numbers, quota bars, and time-series charts stay quiet, scannable, and high-contrast, while the live office stage adds warmth without obscuring data or workspace geometry.

## Typography & Hierarchy
- **Primary Sans:** `Figtree` (`system-ui` fallback), `font-variant-numeric: tabular-nums` on every figure, percentage, dollar amount, and timestamp.
- **Monospace:** `ui-monospace, "SF Mono", Menlo, Consolas, monospace` for CLI commands, local paths, hashes, and model identifiers.
- **Clean Entry:** Headings (`h1`, `h2`) stand on their own without decorative eyebrow pill clusters above them.

## Color & Tonal Surfaces
- **Surfaces:** Soft warm paper (`#f6f7f9` ground, `#ffffff` surface) in light mode; deep slate (`#0b0e13` ground, `#121720` surface) in dark mode. The isometric `.office-stage` preserves its rich dark architectural lighting in both themes (`color-scheme: only dark`).
- **Provider Accents:**
  - **Claude:** Warm terracotta / oak (`#c25927`)
  - **ChatGPT (Codex):** Concrete emerald (`#10a37f`)
  - **Gemini & Antigravity:** Midnight sapphire (`#3b82f6`), with secondary pool bars in calm emerald/violet (`var(--ok)` / `#8b5cf6`).

## Layout, Noise Reduction & Containment
- **No Card-in-Card Clutter:** Lists, quota shares, toggles, and settings rows inside `.card` use clean hairline dividers (`border-bottom: 1px solid var(--line)`) rather than heavy nested boxes.
- **Layout Containment:** Data-heavy `.card` sections below the fold use `content-visibility: auto` (with `contain: layout style paint` fallback) so 60 FPS isometric animations never trigger global page reflows.
- **Non-Occluding Simulation Stage:**
  - **Desk Pills (`.desk-label`):** Compact single-line pills (`● Ad · $Tutar`) anchored above monitors so multi-row cards never cover screens.
  - **Character-Attached Micro Speech Bubbles (`.ofis-balon`):** Instead of wide multi-line speech cards covering desks and monitors, dialogue renders inside compact, translucent micro-pills (`10px` type, `max-width: 132px`, `2px` character-colored edge, `4.5px` pointer tail) anchored directly above each speaker's head while gently repelling from desk pills.
