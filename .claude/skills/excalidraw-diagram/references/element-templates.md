# Element Templates

Copy-paste JSON templates for each Excalidraw element type. The `strokeColor` and `backgroundColor` values are placeholders — always pull actual colors from `color-palette.md` based on the element's semantic purpose.

## Free-Floating Text (no container)
```json
{
  "type": "text",
  "id": "label1",
  "x": 100, "y": 100,
  "width": 200, "height": 25,
  "text": "Section Title",
  "originalText": "Section Title",
  "fontSize": 20,
  "fontFamily": 3,
  "textAlign": "left",
  "verticalAlign": "top",
  "strokeColor": "<title color from palette>",
  "backgroundColor": "transparent",
  "fillStyle": "solid",
  "strokeWidth": 1,
  "strokeStyle": "solid",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 11111,
  "version": 1,
  "versionNonce": 22222,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": null,
  "link": null,
  "locked": false,
  "containerId": null,
  "lineHeight": 1.25
}
```

## Line (structural, not arrow)
```json
{
  "type": "line",
  "id": "line1",
  "x": 100, "y": 100,
  "width": 0, "height": 200,
  "strokeColor": "<structural line color from palette>",
  "backgroundColor": "transparent",
  "fillStyle": "solid",
  "strokeWidth": 2,
  "strokeStyle": "solid",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 44444,
  "version": 1,
  "versionNonce": 55555,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": null,
  "link": null,
  "locked": false,
  "points": [[0, 0], [0, 200]]
}
```

## Small Marker Dot
```json
{
  "type": "ellipse",
  "id": "dot1",
  "x": 94, "y": 94,
  "width": 12, "height": 12,
  "strokeColor": "<marker dot color from palette>",
  "backgroundColor": "<marker dot color from palette>",
  "fillStyle": "solid",
  "strokeWidth": 1,
  "strokeStyle": "solid",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 66666,
  "version": 1,
  "versionNonce": 77777,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": null,
  "link": null,
  "locked": false
}
```

## Rectangle
```json
{
  "type": "rectangle",
  "id": "elem1",
  "x": 100, "y": 100, "width": 180, "height": 90,
  "strokeColor": "<stroke from palette based on semantic purpose>",
  "backgroundColor": "<fill from palette based on semantic purpose>",
  "fillStyle": "solid",
  "strokeWidth": 2,
  "strokeStyle": "solid",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 12345,
  "version": 1,
  "versionNonce": 67890,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": [{"id": "text1", "type": "text"}],
  "link": null,
  "locked": false,
  "roundness": {"type": 3}
}
```

## Text (centered in shape)
```json
{
  "type": "text",
  "id": "text1",
  "x": 130, "y": 132,
  "width": 120, "height": 25,
  "text": "Process",
  "originalText": "Process",
  "fontSize": 16,
  "fontFamily": 3,
  "textAlign": "center",
  "verticalAlign": "middle",
  "strokeColor": "<text color — match parent shape's stroke or use 'on light/dark fills' from palette>",
  "backgroundColor": "transparent",
  "fillStyle": "solid",
  "strokeWidth": 1,
  "strokeStyle": "solid",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 11111,
  "version": 1,
  "versionNonce": 22222,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": null,
  "link": null,
  "locked": false,
  "containerId": "elem1",
  "lineHeight": 1.25
}
```

## Arrow
```json
{
  "type": "arrow",
  "id": "arrow1",
  "x": 282, "y": 145, "width": 118, "height": 0,
  "strokeColor": "<arrow color — typically matches source element's stroke from palette>",
  "backgroundColor": "transparent",
  "fillStyle": "solid",
  "strokeWidth": 2,
  "strokeStyle": "solid",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 33333,
  "version": 1,
  "versionNonce": 44444,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": null,
  "link": null,
  "locked": false,
  "points": [[0, 0], [118, 0]],
  "startBinding": {"elementId": "elem1", "focus": 0, "gap": 2},
  "endBinding": {"elementId": "elem2", "focus": 0, "gap": 2},
  "startArrowhead": null,
  "endArrowhead": "arrow"
}
```

For curves: use 3+ points in `points` array.

---

## Arrow Coordinate System (CRITICAL)

**Understanding how arrow coordinates work is essential to avoid misaligned arrows.**

### Core Rules

1. **`x, y`** = the arrow's absolute starting point on the canvas
2. **`points`** = an array of **relative offsets** from `(x, y)`. The first point **MUST** be `[0, 0]`.
3. **Absolute endpoint** = `(x + points[last][0], y + points[last][1])`
4. **`width`** and **`height`** = bounding box of the points array: `width = max(|p[0]|)`, `height = max(|p[1]|)` across all points

### Example: Straight downward arrow from (400, 200) to (400, 300)

```json
{
  "x": 400, "y": 200,
  "width": 0, "height": 100,
  "points": [[0, 0], [0, 100]]
}
```
- Start: absolute (400, 200)
- End: absolute (400 + 0, 200 + 100) = (400, 300) ✓

### Example: L-shaped arrow from (400, 200) → right to (600, 200) → down to (600, 350)

```json
{
  "x": 400, "y": 200,
  "width": 200, "height": 150,
  "points": [[0, 0], [200, 0], [200, 150]]
}
```
- Start: absolute (400, 200)
- Bend: absolute (400 + 200, 200 + 0) = (600, 200)
- End: absolute (400 + 200, 200 + 150) = (600, 350) ✓

### Example: Arrow going LEFT from (500, 300) to (200, 300)

```json
{
  "x": 500, "y": 300,
  "width": 300, "height": 0,
  "points": [[0, 0], [-300, 0]]
}
```
- Start: absolute (500, 300)
- End: absolute (500 + (-300), 300 + 0) = (200, 300) ✓
- Note: `width` is always positive (absolute value)

### Common Mistake

❌ **Wrong**: Setting `x, y` to an arbitrary point not on the source element's edge
```json
{
  "x": 450, "y": 250,
  "points": [[0, 0], [0, 100]]
}
```
This arrow starts at (450, 250) which may be floating in space, not connected to any element!

✅ **Correct**: Calculate the source element's edge midpoint first, then use that as `x, y`
```json
// Rectangle at (360, 200, w=180, h=90) → bottom midpoint = (360+90, 200+90) = (450, 290)
{
  "x": 450, "y": 290,
  "points": [[0, 0], [0, 60]]
}
```

---

## Element Edge Connection Points

**When connecting arrows to elements, use these formulas to find edge midpoints.**

All formulas assume an element defined as `(x, y, width, height)` where `(x, y)` is the top-left corner.

### Rectangle / Diamond / Ellipse (bounding box edges)

| Edge | Formula | Description |
|------|---------|-------------|
| **Top center** | `(x + w/2, y)` | Middle of top edge |
| **Bottom center** | `(x + w/2, y + h)` | Middle of bottom edge |
| **Left center** | `(x, y + h/2)` | Middle of left edge |
| **Right center** | `(x + w, y + h/2)` | Middle of right edge |
| **Center** | `(x + w/2, y + h/2)` | Center of element |

### Helper Functions (for Python generator scripts)

When generating large diagrams (50+ elements) with a script, define these helpers:

```python
def top(b):    return (b[0] + b[2]/2, b[1])          # top center
def bot(b):    return (b[0] + b[2]/2, b[1] + b[3])   # bottom center
def lft(b):    return (b[0],          b[1] + b[3]/2)  # left center
def rgt(b):    return (b[0] + b[2],   b[1] + b[3]/2)  # right center
```

Where `b = (x, y, width, height)` is the tuple returned by the element creation function.

### Usage Pattern

```python
# Create two boxes
box_a = add_rect('a', 100, 100, 180, 60, ...)  # returns (100, 100, 180, 60)
box_b = add_rect('b', 100, 220, 180, 60, ...)  # returns (100, 220, 180, 60)

# Arrow from bottom of A to top of B
add_arrow('a_to_b', *bot(box_a), *top(box_b), '#333')
# This creates: start=(190, 160), end=(190, 220) — perfectly connected!
```

### Multi-Branch from Diamond

When a diamond has 3+ outgoing branches, use different edges:

```
          (top)
            ↑
  (left) ← ◇ → (right)
            ↓
         (bottom)
```

- Branch 1: from `lft(diamond)` → route left
- Branch 2: from `rgt(diamond)` → route right
- Branch 3: from `bot(diamond)` → route down (then turn if needed)

**Every arrow MUST start from a point ON an element's edge.** An arrow starting in empty space is always a bug.

---

## Module Expansion Container (Frame)
Use for Level 4 module drill-down areas. Wraps all internal elements of an expanded module.
```json
{
  "type": "rectangle",
  "id": "module_expand_container",
  "x": 100, "y": 400, "width": 600, "height": 350,
  "strokeColor": "<depth L2 stroke from palette>",
  "backgroundColor": "<depth L2 background from palette>",
  "fillStyle": "solid",
  "strokeWidth": 1,
  "strokeStyle": "dashed",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 500001,
  "version": 1,
  "versionNonce": 500002,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": [{"id": "module_expand_title", "type": "text"}],
  "link": null,
  "locked": false,
  "roundness": {"type": 3}
}
```

## Module Expansion Title
Title text for the expansion container, placed at the top-left inside it.
```json
{
  "type": "text",
  "id": "module_expand_title",
  "x": 110, "y": 408,
  "width": 300, "height": 20,
  "text": "① Module Name — Internal Detail",
  "originalText": "① Module Name — Internal Detail",
  "fontSize": 16,
  "fontFamily": 3,
  "textAlign": "left",
  "verticalAlign": "top",
  "strokeColor": "<title color from palette>",
  "backgroundColor": "transparent",
  "fillStyle": "solid",
  "strokeWidth": 1,
  "strokeStyle": "solid",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 500003,
  "version": 1,
  "versionNonce": 500004,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": null,
  "link": null,
  "locked": false,
  "containerId": null,
  "lineHeight": 1.25
}
```

## Internal Step (Small Rectangle)
Use inside module expansion areas. Smaller than overview-level rectangles.
```json
{
  "type": "rectangle",
  "id": "internal_step_1",
  "x": 130, "y": 460, "width": 100, "height": 50,
  "strokeColor": "<depth L3 stroke from palette>",
  "backgroundColor": "<depth L3 fill from palette>",
  "fillStyle": "solid",
  "strokeWidth": 1,
  "strokeStyle": "solid",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 500010,
  "version": 1,
  "versionNonce": 500011,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": [{"id": "internal_step_1_text", "type": "text"}],
  "link": null,
  "locked": false,
  "roundness": {"type": 3}
}
```

## Code Path Annotation
Free-floating text placed directly below a module or internal step to show source file paths.
```json
{
  "type": "text",
  "id": "codepath_module1",
  "x": 100, "y": 200,
  "width": 180, "height": 30,
  "text": "rag/retriever.go\nrag/retriever_reranker.go",
  "originalText": "rag/retriever.go\nrag/retriever_reranker.go",
  "fontSize": 12,
  "fontFamily": 3,
  "textAlign": "left",
  "verticalAlign": "top",
  "strokeColor": "<body/detail color from palette>",
  "backgroundColor": "transparent",
  "fillStyle": "solid",
  "strokeWidth": 1,
  "strokeStyle": "solid",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 600001,
  "version": 1,
  "versionNonce": 600002,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": null,
  "link": null,
  "locked": false,
  "containerId": null,
  "lineHeight": 1.25
}
```

## Connection Callout (Dashed Arrow to Expansion)
Dashed arrow linking an overview module to its expansion area.
```json
{
  "type": "arrow",
  "id": "expand_link_1",
  "x": 190, "y": 195, "width": 0, "height": 195,
  "strokeColor": "<depth L2 stroke from palette>",
  "backgroundColor": "transparent",
  "fillStyle": "solid",
  "strokeWidth": 1,
  "strokeStyle": "dashed",
  "roughness": 0,
  "opacity": 100,
  "angle": 0,
  "seed": 700001,
  "version": 1,
  "versionNonce": 700002,
  "isDeleted": false,
  "groupIds": [],
  "boundElements": null,
  "link": null,
  "locked": false,
  "points": [[0, 0], [0, 195]],
  "startBinding": {"elementId": "overview_module_rect", "focus": 0, "gap": 5},
  "endBinding": {"elementId": "module_expand_container", "focus": 0, "gap": 5},
  "startArrowhead": null,
  "endArrowhead": "arrow"
}
```
