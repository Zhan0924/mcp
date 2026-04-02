# Color Palette & Brand Style

**This is the single source of truth for all colors and brand-specific styles.** To customize diagrams for your own brand, edit this file — everything else in the skill is universal.

---

## Shape Colors (Semantic)

Colors encode meaning, not decoration. Each semantic purpose has a fill/stroke pair.

| Semantic Purpose | Fill | Stroke |
|------------------|------|--------|
| Primary/Neutral | `#3b82f6` | `#1e3a5f` |
| Secondary | `#60a5fa` | `#1e3a5f` |
| Tertiary | `#93c5fd` | `#1e3a5f` |
| Start/Trigger | `#fed7aa` | `#c2410c` |
| End/Success | `#a7f3d0` | `#047857` |
| Warning/Reset | `#fee2e2` | `#dc2626` |
| Decision | `#fef3c7` | `#b45309` |
| AI/LLM | `#ddd6fe` | `#6d28d9` |
| Inactive/Disabled | `#dbeafe` | `#1e40af` (use dashed stroke) |
| Error | `#fecaca` | `#b91c1c` |

**Rule**: Always pair a darker stroke with a lighter fill for contrast.

---

## Text Colors (Hierarchy)

Use color on free-floating text to create visual hierarchy without containers.

| Level | Color | Use For |
|-------|-------|---------|
| Title | `#1e40af` | Section headings, major labels |
| Subtitle | `#3b82f6` | Subheadings, secondary labels |
| Body/Detail | `#64748b` | Descriptions, annotations, metadata |
| On light fills | `#374151` | Text inside light-colored shapes |
| On dark fills | `#ffffff` | Text inside dark-colored shapes |

---

## Evidence Artifact Colors

Used for code snippets, data examples, and other concrete evidence inside technical diagrams.

| Artifact | Background | Text Color |
|----------|-----------|------------|
| Code snippet | `#1e293b` | Syntax-colored (language-appropriate) |
| JSON/data example | `#1e293b` | `#22c55e` (green) |

---

## Default Stroke & Line Colors

| Element | Color |
|---------|-------|
| Arrows | Use the stroke color of the source element's semantic purpose |
| Structural lines (dividers, trees, timelines) | Primary stroke (`#1e3a5f`) or Slate (`#64748b`) |
| Marker dots (fill + stroke) | Primary fill (`#3b82f6`) |

---

## Depth Level Colors (For Module Drill-Down)

When expanding modules to show internal details, use progressively lighter/muted colors to create visual depth hierarchy. Deeper levels appear visually "further away" from the overview.

| Depth Level | Stroke | Fill/Background | Border Style | Use For |
|-------------|--------|-----------------|--------------|---------|
| L1 Overview | Use semantic colors (above) | `#ffffff` | solid, strokeWidth 2 | Main diagram modules |
| L2 Expansion Container | `#94a3b8` | `#f8fafc` | **dashed**, strokeWidth 1 | Module expansion frames |
| L3 Internal Steps | `#94a3b8` | `#f1f5f9` | solid, strokeWidth 1 | Sub-steps inside expanded modules |
| L4 Internal Detail | `#cbd5e1` | transparent | solid, strokeWidth 1 | Finest-grain elements within modules |

**Code Path Annotation Color**: Use the Body/Detail text color (`#64748b`) at `fontSize: 12` for all code path annotations below modules.

**Rule**: The overview level (L1) uses full semantic colors. Each deeper level uses progressively more muted grays. This naturally communicates "this is a detail of something above."

---

## Background

| Property | Value |
|----------|-------|
| Canvas background | `#ffffff` |
