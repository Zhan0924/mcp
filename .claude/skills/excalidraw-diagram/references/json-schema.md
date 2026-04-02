# Excalidraw JSON Schema

## Element Types

| Type | Use For |
|------|---------|
| `rectangle` | Processes, actions, components |
| `ellipse` | Entry/exit points, external systems |
| `diamond` | Decisions, conditionals |
| `arrow` | Connections between shapes |
| `text` | Labels inside shapes |
| `line` | Non-arrow connections |
| `frame` | Grouping containers |

## Common Properties

All elements share these:

| Property | Type | Description |
|----------|------|-------------|
| `id` | string | Unique identifier |
| `type` | string | Element type |
| `x`, `y` | number | Position in pixels |
| `width`, `height` | number | Size in pixels |
| `strokeColor` | string | Border color (hex) |
| `backgroundColor` | string | Fill color (hex or "transparent") |
| `fillStyle` | string | "solid", "hachure", "cross-hatch" |
| `strokeWidth` | number | 1, 2, or 4 |
| `strokeStyle` | string | "solid", "dashed", "dotted" |
| `roughness` | number | 0 (smooth), 1 (default), 2 (rough) |
| `opacity` | number | 0-100 |
| `seed` | number | Random seed for roughness |

## Text-Specific Properties

| Property | Description |
|----------|-------------|
| `text` | The display text |
| `originalText` | Same as text |
| `fontSize` | Size in pixels (16-20 recommended) |
| `fontFamily` | 3 for monospace (use this) |
| `textAlign` | "left", "center", "right" |
| `verticalAlign` | "top", "middle", "bottom" |
| `containerId` | ID of parent shape |

## Arrow-Specific Properties

| Property | Description |
|----------|-------------|
| `points` | Array of [x, y] coordinates |
| `startBinding` | Connection to start shape |
| `endBinding` | Connection to end shape |
| `startArrowhead` | null, "arrow", "bar", "dot", "triangle" |
| `endArrowhead` | null, "arrow", "bar", "dot", "triangle" |

## Binding Format

```json
{
  "elementId": "shapeId",
  "focus": 0,
  "gap": 2
}
```

## Rectangle Roundness

Add for rounded corners:
```json
"roundness": { "type": 3 }
```

## Frame (Grouping Container)

Frames are used to visually group related elements. Particularly useful for module expansion areas (Level 4 drill-down).

| Property | Type | Description |
|----------|------|-------------|
| `type` | string | `"frame"` |
| `name` | string | Display name shown at the top of the frame |

**Note:** In practice, for broader compatibility, module expansion containers are often implemented as large `rectangle` elements with `strokeStyle: "dashed"` and a free-floating title text element, rather than the native `frame` type. Both approaches work — the rectangle approach gives more control over styling (fill color, border style, roundness).

### Frame as Rectangle Pattern
```json
{
  "type": "rectangle",
  "strokeStyle": "dashed",
  "strokeWidth": 1,
  "backgroundColor": "#f8fafc",
  "roundness": { "type": 3 }
}
```
Pair with a free-floating text element positioned at the top-left as the "title".

## Depth-Scaled Sizing Reference

When building module internals (Level 4), elements should be sized smaller than their overview counterparts:

| Diagram Level | Rectangle Size | Font Size | Stroke Width |
|---------------|---------------|-----------|--------------|
| L1 Overview — Hero | 300×150 | 20-24 | 2-3 |
| L1 Overview — Primary | 180×90 | 16 | 2 |
| L2 Expansion Container | 500-700 wide × auto | 16 (title) | 1 (dashed) |
| L3 Internal Steps | 100×50 | 14 | 1 |
| L4 Internal Detail | 60×40 | 12 | 1 |
| Code Path Annotations | n/a (text only) | 12 | n/a |
